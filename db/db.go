package db

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/raisemarketplace/kubesat/logger"
)

type Snapshot struct {
	ClusterName      string
	ClusterPodCounts PodCounts
	NodeCount        int
	NodeTable        *NodeTable
}

type NamespaceName struct {
	Namespace string
	Name      string
}

type KubernetesData struct {
	Nodes     []v1.Node
	Pods      []v1.Pod
	Endpoints map[NamespaceName]v1.Endpoints
}

type AwsData struct {
	Instances []ec2.Instance
	Images    []*ec2.Image
}

type PodCounts struct {
	Total     int
	Pending   int
	Running   int
	Succeeded int
	Failed    int
	Unknown   int
}

type DB struct {
	logger    *logger.Logger
	clientset *kubernetes.Clientset
	ec2client *ec2.EC2

	Snapshots <-chan Snapshot

	kubernetes chan KubernetesData
	aws        chan AwsData

	kubernetesData KubernetesData
	awsData        AwsData

	nodeRequest chan chan<- []v1.Node
}

func NewDB(logger *logger.Logger, clientset *kubernetes.Clientset, ec2client *ec2.EC2) *DB {
	snapshots := make(chan Snapshot)

	db := DB{
		logger:    logger,
		clientset: clientset,
		ec2client: ec2client,

		Snapshots:  snapshots,
		kubernetes: make(chan KubernetesData),
		aws:        make(chan AwsData),

		kubernetesData: KubernetesData{},
		awsData:        AwsData{},

		nodeRequest: make(chan chan<- []v1.Node),
	}

	go db.serve(snapshots)

	return &db
}

// FetchPods requests pods from all namespaces and returns a merged list.
func FetchPods(clientset *kubernetes.Clientset) ([]v1.Pod, error) {
	namespaces, err := clientset.CoreV1().Namespaces().List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error listing namespaces: %s", err)
	}

	allPods := make([]v1.Pod, 0)

	for _, namespace := range namespaces.Items {
		pods, err := clientset.CoreV1().Pods(namespace.Name).List(metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("error listing pods in namespace %v: %s", namespace, err)
		}

		allPods = append(allPods, pods.Items...)
	}

	return allPods, nil
}

// FetchNodes requests all nodes and returns the items without list metadata.
func FetchNodes(clientset *kubernetes.Clientset) ([]v1.Node, error) {
	nodes, err := clientset.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return nodes.Items, nil
}

// FetchEndpoints requests the given endpoints.
func FetchEndpoints(clientset *kubernetes.Clientset, names []NamespaceName) (map[NamespaceName]v1.Endpoints, error) {
	eps := make(map[NamespaceName]v1.Endpoints)
	for _, name := range names {
		ep, err := clientset.CoreV1().Endpoints(name.Namespace).Get(name.Name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("error getting %s:ep/%s: %s", name.Namespace, name.Name, err)
		}
		eps[name] = *ep
	}
	return eps, nil
}

func FetchKubernetes(clientset *kubernetes.Clientset) (KubernetesData, error) {
	nodes, err := FetchNodes(clientset)
	if err != nil {
		return KubernetesData{}, err
	}

	pods, err := FetchPods(clientset)
	if err != nil {
		return KubernetesData{}, err
	}

	epNames := []NamespaceName{{"kube-system", "kube-controller-manager"}, {"kube-system", "kube-scheduler"}}
	eps, err := FetchEndpoints(clientset, epNames)
	if err != nil {
		return KubernetesData{}, err
	}

	return KubernetesData{Nodes: nodes, Pods: pods, Endpoints: eps}, nil
}

func FetchAws(ec2client *ec2.EC2, nodes []v1.Node) (AwsData, error) {
	instances, err := FetchInstances(ec2client, nodes)
	if err != nil {
		return AwsData{}, err
	}

	images, err := FetchImages(ec2client, instances)
	if err != nil {
		return AwsData{}, err
	}

	return AwsData{Instances: instances, Images: images}, nil
}

func (db *DB) loopKubernetes() {
	for ; ; time.Sleep(2 * time.Second) {
		kubernetesData, err := FetchKubernetes(db.clientset)
		if err != nil {
			db.logger.Errorf("error fetching kubernetes: %v", err)
			continue
		}

		db.Kubernetes() <- kubernetesData
	}
}

func (db *DB) loopAws(initialNodes []v1.Node) {
	nodes := initialNodes

	for ; ; time.Sleep(10 * time.Second) {
		awsData, err := FetchAws(db.ec2client, nodes)
		if err != nil {
			db.logger.Errorf("error fetching aws: %v", err)
			continue
		}

		db.Aws() <- awsData

		var ok bool
		nodes, ok = db.Nodes()
		if !ok {
			db.logger.Errorf("error fetching existing nodes: %v", err)
			continue
		}
	}
}

func (db *DB) serve(snapshots chan<- Snapshot) {
	haveFirstKubernetesData := false

	// first time through, start kubernetes loop
	go db.loopKubernetes()

	for {
		select {
		case data := <-db.kubernetes:
			db.kubernetesData = data

			// delay starting aws loop until we have some
			// kubernetes data; ensures first node table
			// is likely to join both kubernetes and aws
			if !haveFirstKubernetesData {
				haveFirstKubernetesData = true
				go db.loopAws(db.kubernetesData.Nodes)
				continue
			}

			snapshots <- Join(db.kubernetesData, db.awsData)
		case data := <-db.aws:
			db.awsData = data
			snapshots <- Join(db.kubernetesData, db.awsData)
		case replyChan := <-db.nodeRequest:
			replyChan <- db.kubernetesData.Nodes
		}
	}
}

func (db *DB) Kubernetes() chan<- KubernetesData {
	return db.kubernetes
}

func (db *DB) Aws() chan<- AwsData {
	return db.aws
}

func (db *DB) Nodes() ([]v1.Node, bool) {
	reply := make(chan []v1.Node)
	db.nodeRequest <- reply
	nodes, ok := <-reply
	return nodes, ok
}

func Join(kubernetesData KubernetesData, awsData AwsData) Snapshot {
	nodeTable := NewNodeTable()

	controllerManagerLeader, err := LeaderHolderIdentity(kubernetesData.Endpoints[NamespaceName{"kube-system", "kube-controller-manager"}])
	haveControllerManagerLeader := (err == nil)
	schedulerLeader, err := LeaderHolderIdentity(kubernetesData.Endpoints[NamespaceName{"kube-system", "kube-scheduler"}])
	haveSchedulerLeader := (err == nil)

	// pods counts
	clusterCounts := PodCounts{Total: len(kubernetesData.Pods)}
	nodesCounts := make(map[string]*PodCounts)
	for _, pod := range kubernetesData.Pods {
		nodeCounts, ok := nodesCounts[pod.Spec.NodeName]
		if !ok {
			nodeCounts = &PodCounts{}
			nodesCounts[pod.Spec.NodeName] = nodeCounts
		}

		switch pod.Status.Phase {
		case v1.PodPending:
			clusterCounts.Pending++
			nodeCounts.Pending++
			nodeCounts.Total++
		case v1.PodRunning:
			clusterCounts.Running++
			nodeCounts.Running++
			nodeCounts.Total++
		case v1.PodSucceeded:
			clusterCounts.Succeeded++
			nodeCounts.Succeeded++
			nodeCounts.Total++
		case v1.PodFailed:
			clusterCounts.Failed++
			nodeCounts.Failed++
			nodeCounts.Total++
		case v1.PodUnknown:
			clusterCounts.Unknown++
			nodeCounts.Unknown++
			nodeCounts.Total++
		default:
			// TODO log? add to Unknown?
			nodeCounts.Total++
		}
	}

	// fill nodes
	for _, node := range kubernetesData.Nodes {
		nodeTable.WithRowAtName(node.Name, func(row *NodeTableRow) {
			if v, ok := node.ObjectMeta.Labels["kubernetes.io/role"]; ok && v == "master" {
				row.IsMaster = true
			}
			if haveControllerManagerLeader && strings.HasPrefix(node.Name, controllerManagerLeader) {
				row.IsControllerManagerLeader = true
			}
			if haveSchedulerLeader && strings.HasPrefix(node.Name, schedulerLeader) {
				row.IsSchedulerLeader = true
			}
			row.IsCordoned = node.Spec.Unschedulable
			row.CreatedAt = node.ObjectMeta.CreationTimestamp.Time
			row.KubeletVersion = node.Status.NodeInfo.KubeletVersion
			if counts, ok := nodesCounts[node.Name]; ok {
				row.PodCounts = *counts
			}

			row.AwsID = node.Spec.ExternalID
		})
	}

	// grab cluster name from first instance (could it be missing? incorrect?)
	clusterName := ""
	if len(awsData.Instances) > 0 {
		instance := awsData.Instances[0]
		for _, tag := range instance.Tags {
			if *tag.Key == "KubernetesCluster" {
				clusterName = *tag.Value
				break
			}
		}
	}
	// fill instances
	for _, instance := range awsData.Instances {

		nodeTable.WithRowAtAwsID(*instance.InstanceId, func(row *NodeTableRow) {
			row.ImageID = *instance.ImageId
			row.AwsState = *instance.State.Name
		})
	}

	// fill images
	images := make(map[string]ec2.Image)
	for _, image := range awsData.Images {
		images[*image.ImageId] = *image
	}
	for _, row := range nodeTable.Rows {
		image, ok := images[row.ImageID]
		if !ok {
			continue
		}

		// FIXME: awfully raise specific...
		for _, tag := range image.Tags {
			if *tag.Key == "version" {
				row.ImageVersion = *tag.Value
				break
			}
		}
	}

	nodeTable.Sort()

	return Snapshot{
		ClusterName:      clusterName,
		ClusterPodCounts: clusterCounts,
		NodeCount:        len(kubernetesData.Nodes),
		NodeTable:        nodeTable,
	}
}

// FetchInstances gets the AWS instances references by the given
// nodes. It also extracts the tag KubernetesCluster from the
// resulting instances, and fetches all instances with the same
// KubernetesCluster tag, returning a merged list (without
// duplicates).
func FetchInstances(ec2client *ec2.EC2, nodes []v1.Node) ([]ec2.Instance, error) {
	instances := make([]ec2.Instance, 0, len(nodes))

	// instance ids already present in instances slice
	seen := make(map[string]bool)

	// cluster names extracted from instances
	clusters := make(map[string]bool)

	// fetch instances corresponding to nodes
	ids := make([]*string, 0, len(nodes))
	for _, node := range nodes {
		if node.Spec.ExternalID == "" {
			continue
		}
		ids = append(ids, &node.Spec.ExternalID)
	}
	output, err := ec2client.DescribeInstances(&ec2.DescribeInstancesInput{InstanceIds: ids})
	if err != nil {
		return nil, err
	}
	for _, res := range output.Reservations {
		for _, inst := range res.Instances {
			instances = append(instances, *inst)
			seen[*inst.InstanceId] = true

			for _, tag := range inst.Tags {
				if *tag.Key == "KubernetesCluster" {
					clusters[*tag.Value] = true
					break
				}
			}
		}
	}

	// fetch instances corresponding to observed cluster tags
	clusterKeys := make([]*string, 0, len(clusters))
	for key, _ := range clusters {
		clusterKeys = append(clusterKeys, &key)
	}
	name := "tag:KubernetesCluster"
	output, err = ec2client.DescribeInstances(&ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			&ec2.Filter{Name: &name, Values: clusterKeys}}})
	if err != nil {
		return nil, err
	}
	for _, res := range output.Reservations {
		for _, inst := range res.Instances {
			if _, ok := seen[*inst.InstanceId]; ok {
				continue
			}
			instances = append(instances, *inst)
		}
	}

	return instances, nil
}

// FetchImages fetches ec2 images used by the given instances.
func FetchImages(ec2client *ec2.EC2, instances []ec2.Instance) ([]*ec2.Image, error) {
	imageIDs := make(map[string]bool)

	for _, instance := range instances {
		if instance.ImageId == nil {
			continue
		}
		imageIDs[*instance.ImageId] = true
	}

	imageIDKeys := make([]*string, 0, len(imageIDs))
	for key, _ := range imageIDs {
		copy := key
		imageIDKeys = append(imageIDKeys, &copy)
	}

	output, err := ec2client.DescribeImages(&ec2.DescribeImagesInput{ImageIds: imageIDKeys})
	if err != nil {
		return nil, err
	}

	return output.Images, nil
}

func LeaderHolderIdentity(ep v1.Endpoints) (string, error) {
	annotation := "control-plane.alpha.kubernetes.io/leader"
	jsonString, ok := ep.ObjectMeta.Annotations[annotation]
	if !ok {
		return "", fmt.Errorf("no annotation present: %s", annotation)
	}

	var info map[string]interface{}
	if err := json.Unmarshal([]byte(jsonString), &info); err != nil {
		return "", fmt.Errorf("error unmarshling %s json data: %s", annotation, err)
	}

	holderIdentity, ok := info["holderIdentity"]
	if !ok {
		return "", fmt.Errorf("holderIdentity not found in %s data", annotation)
	}

	if id, ok := holderIdentity.(string); ok {
		return id, nil
	}

	return "", fmt.Errorf("expected holderIdentity string but found %t", holderIdentity)
}
