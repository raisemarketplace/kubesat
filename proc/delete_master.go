package proc

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"

	"k8s.io/api/core/v1"
	"k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type status struct {
	finished bool
	msg      string
}

type ProcContext struct {
	parent   context.Context
	cancel   context.CancelFunc
	finished atomic.Value
	status   atomic.Value
	subprocs []Proc
}

// Deadline implements Context interface
func (ctx *ProcContext) Deadline() (time.Time, bool) {
	return ctx.parent.Deadline()
}

// Done implements Context interface
func (ctx *ProcContext) Done() <-chan struct{} {
	return ctx.parent.Done()
}

// Err implements Context interface
func (ctx *ProcContext) Err() error {
	return ctx.parent.Err()
}

// Value implements Context interface
func (ctx *ProcContext) Value(key interface{}) interface{} {
	return ctx.parent.Value(key)
}

func (ctx *ProcContext) SetStatus(format string, args ...interface{}) {
	ctx.status.Store(status{msg: fmt.Sprintf(format, args...)})
}

func (ctx *ProcContext) Finish(format string, args ...interface{}) {
	ctx.status.Store(status{finished: true, msg: fmt.Sprintf(format, args...)})
}

func (ctx *ProcContext) IsRunning() bool {
	return ctx.Err() == nil
}

func (ctx *ProcContext) Status() string {
	status, ok := ctx.status.Load().(status)
	if !ok {
		status.msg = "unknown"
	}

	err := ctx.Err()
	switch err {
	case context.Canceled:
		if status.finished {
			return fmt.Sprintf("finished: %s", status.msg)
		} else {
			return fmt.Sprintf("cancelled: %s", status.msg)
		}
	case context.DeadlineExceeded:
		return fmt.Sprintf("deadline exceeded: %s", status.msg)
	default:
		return status.msg
	}
}

// TODO
func (ctx *ProcContext) Subprocs() []Proc {
	return nil
}

func (ctx *ProcContext) push(p *Proc) {
}

type DeleteMaster struct {
	ProcName string

	name      string
	ctx       *ProcContext
	clientset *kubernetes.Clientset
	ec2client *ec2.EC2
}

func RunDeleteMaster(ctx0 context.Context, clientset *kubernetes.Clientset, ec2client *ec2.EC2, name string) *DeleteMaster {
	ctx, cancel := context.WithCancel(ctx0)
	proc := &DeleteMaster{
		ProcName:  fmt.Sprintf("delete-master:%s", name),
		name:      name,
		ctx:       &ProcContext{parent: ctx, cancel: cancel},
		clientset: clientset,
		ec2client: ec2client,
	}
	go proc.run()
	return proc
}

func (proc *DeleteMaster) Name() string {
	return proc.ProcName
}

func (proc *DeleteMaster) Status() (string, bool) {
	return proc.ctx.Status(), proc.ctx.IsRunning()
}

func (proc *DeleteMaster) run() {
	defer proc.ctx.cancel()

	proc.ctx.SetStatus("starting...")

	nodes := proc.clientset.CoreV1().Nodes()

	node, err := nodes.Get(proc.name, metav1.GetOptions{})
	if err != nil {
		proc.ctx.SetStatus("error getting node %s: %s", proc.name, err)
		return
	}

	proc.ctx.SetStatus("cordoning node %s", proc.name)
	node.Spec.Unschedulable = true
	node, err = nodes.Update(node)

	proc.ctx.SetStatus("finding pods...")
	pods := make([]v1.Pod, 0)
	namespaces, err := proc.clientset.CoreV1().Namespaces().List(metav1.ListOptions{})
	if err != nil {
		proc.ctx.SetStatus("error listing namespaces: %s", err)
		return
	}
	for _, namespace := range namespaces.Items {
		result, err := proc.clientset.CoreV1().Pods(namespace.Name).List(metav1.ListOptions{})
		if err != nil {
			proc.ctx.SetStatus("error listing pods in namespace %v: %s", namespace, err)
			return
		}
		for _, pod := range result.Items {
			if pod.Spec.NodeName != node.Name {
				continue
			}
			pods = append(pods, pod)
		}
	}

	proc.ctx.SetStatus("evicting %d pods...", len(pods))
	for i, pod := range pods {
		proc.ctx.SetStatus("%d/%d evicting pod (%s) %s", i+1, len(pods), pod.Namespace, pod.Name)
		eviction := v1beta1.Eviction{ObjectMeta: pod.ObjectMeta}
		err = proc.clientset.CoreV1().Pods(pod.Namespace).Evict(&eviction)
		if err != nil {
			proc.ctx.SetStatus("error evicting pod (%s) %s: %v", pod.Namespace, pod.Name, err)
			return
		}
	}

	proc.ctx.SetStatus("terminating ec2 instance %s", node.Spec.ExternalID)
	_, err = proc.ec2client.TerminateInstances(&ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(node.Spec.ExternalID)},
	})
	if err != nil {
		proc.ctx.SetStatus("error terminating ec2 instance %s: %v", node.Spec.ExternalID, err)
		return
	}
	proc.ctx.SetStatus("terminated ec2 instance %s", node.Spec.ExternalID)

	// Pre 1.10, the "kubernetes" endpoint will have stale IPs
	// when a master node is terminated before the replacement
	// comes online. For in-cluster pods accessing the
	// "kubernetes" service, this can result in failures. So we
	// delete the terminated IP manually.
	//
	// https://kubernetes.io/docs/admin/high-availability/building/#endpoint-reconciler
	//
	// TODO does InternalIP apply everywhere?
	if v, ok := node.ObjectMeta.Labels["kubernetes.io/role"]; ok && v == "master" {
		proc.ctx.SetStatus("removing node %s from ep/kubernetes", node.ObjectMeta.Name)

		internalIP, ok := func() (string, bool) {
			for _, addr := range node.Status.Addresses {
				if addr.Type == "InternalIP" {
					return addr.Address, true
				}
			}
			return "", false
		}()
		if !ok {
			proc.ctx.SetStatus("failed to find InternalIP for node %s", node.ObjectMeta.Name)
			return
		}

		proc.ctx.SetStatus("removing InternalIP %s from ep/kubernetes", internalIP)

		endpoints := proc.clientset.CoreV1().Endpoints("default")
		for {
			modified := false

			ep, err := endpoints.Get("kubernetes", metav1.GetOptions{})
			if err != nil {
				proc.ctx.SetStatus("error getting ep/kubernetes: %v", err)
				return
			}

			for i, subset := range ep.Subsets {
				newAddrs := make([]v1.EndpointAddress, 0, len(subset.Addresses))
				for _, addr := range subset.Addresses {
					// drop the one that matches terminated node's internal IP
					if addr.IP == internalIP {
						modified = true
						continue
					}
					newAddrs = append(newAddrs, addr)
				}
				ep.Subsets[i].Addresses = newAddrs
			}

			ep, err = endpoints.Update(ep)
			if err != nil {
				proc.ctx.SetStatus("error updating ep/kubernetes: %v", err)
				return
			}

			if !modified {
				break
			}

			watch, err := endpoints.Watch(metav1.ListOptions{
				FieldSelector:   "metadata.name=kubernetes",
				Watch:           true,
				ResourceVersion: ep.ObjectMeta.ResourceVersion,
			})
			if err != nil {
				proc.ctx.SetStatus("error watching ep/kubernetes for further changes: %v", err)
				return
			}
			defer watch.Stop()
			select {
			case ev := <-watch.ResultChan():
				proc.ctx.SetStatus("ep/kubernetes event: %s", ev.Type)
			case <-time.After(10 * time.Second):
				proc.ctx.SetStatus("no change to ep/kubernetes for 10s")
			}
		}
	}

	proc.ctx.Finish("done!")
}
