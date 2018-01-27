package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	termbox "github.com/nsf/termbox-go"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"

	v1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	// _ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	"github.com/raisemarketplace/kubesat/db"
	"github.com/raisemarketplace/kubesat/logger"
	"github.com/raisemarketplace/kubesat/termbox/kit"
)

// const Banana rune = '⦅' // unicode left white paren
const Banana rune = '❪' // unicode MEDIUM FLATTENED LEFT PARENTHESIS ORNAMENT

// BananaColors for termbox.Output256
// https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit
var BananaColors = [6]termbox.Attribute{
	0x2f, // very green
	0x9b, // yellowish-green
	0xbf, // not quite ripe
	0xb9, // ripe
	0x89, // brownish-yellow
	0x5f, // banana bread
}

// BananaAges where anything less than the given age corresponds to
// the same index in BananaColors, with everything else falling to the
// last color.
var BananaAges = [5]time.Duration{
	10 * time.Minute,
	30 * time.Minute,
	4 * time.Hour,
	12 * time.Hour,
	5 * 24 * time.Hour,
}

// AgeColor returns a color for termbox.Output256 for the given age on
// an arbitrary "banana" scale.
func AgeColor(age time.Duration) termbox.Attribute {
	for i, cutoff := range BananaAges {
		if age < cutoff {
			return BananaColors[i]
		}
	}
	return BananaColors[len(BananaColors)-1]
}

type Data struct {
	Nodes     []int
	Pods      []v1.Pod
	NodeTable *db.NodeTable
}

func FillPodCounts(nt *db.NodeTable, clientset *kubernetes.Clientset) error {
	namespaces, err := clientset.CoreV1().Namespaces().List(metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing namespaces: %s", err)
	}

	for _, namespace := range namespaces.Items {
		pods, err := clientset.CoreV1().Pods(namespace.Name).List(metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("error listing pods in namespace %s: %s", namespace, err)
		}

		for _, pod := range pods.Items {
			if pod.Spec.NodeName == "" {
				continue
			}

			nt.WithRowAtName(pod.Spec.NodeName, func(row *db.NodeTableRow) {
				switch pod.Status.Phase {
				case v1.PodPending:
					row.PodsPendingCount++
				case v1.PodRunning:
					row.PodsRunningCount++
				case v1.PodSucceeded:
					row.PodsSucceededCount++
				case v1.PodFailed:
					row.PodsFailedCount++
				case v1.PodUnknown:
					row.PodsUnknownCount++
				default:
					// TODO log?
				}
			})
		}
	}

	return nil
}

func FillNodes(nt *db.NodeTable, clientset *kubernetes.Clientset) error {
	nodes, err := clientset.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		nt.WithRowAtName(node.Name, func(row *db.NodeTableRow) {
			if v, ok := node.ObjectMeta.Labels["kubernetes.io/role"]; ok && v == "master" {
				row.IsMaster = true
			}
			row.IsCordoned = node.Spec.Unschedulable
			row.CreatedAt = node.ObjectMeta.CreationTimestamp.Time
			row.KubeletVersion = node.Status.NodeInfo.KubeletVersion
			row.AwsID = node.Spec.ExternalID
		})
	}

	return nil
}

// FillLeaderFlags add flags to the controller-manager and scheduler leaders.
func FillLeaderFlags(nt *db.NodeTable, clientset *kubernetes.Clientset) error {
	controllerManagerLeader, err := LeaderHolderIdentity(clientset, "kube-controller-manager")
	if err != nil {
		return err
	}

	schedulerLeader, err := LeaderHolderIdentity(clientset, "kube-scheduler")
	if err != nil {
		return err
	}

	for _, row := range nt.Rows {
		if strings.HasPrefix(row.Name, controllerManagerLeader) {
			row.IsControllerManagerLeader = true
			break
		}
	}
	for _, row := range nt.Rows {
		if strings.HasPrefix(row.Name, schedulerLeader) {
			row.IsSchedulerLeader = true
			break
		}
	}

	return nil
}

func fillInstance(nt *db.NodeTable, inst *ec2.Instance) {
	nt.WithRowAtAwsID(*inst.InstanceId, func(row *db.NodeTableRow) {
		row.ImageID = *inst.ImageId
		row.AwsState = *inst.State.Name

		for _, tag := range inst.Tags {
			if *tag.Key == "KubernetesCluster" {
				row.KubernetesCluster = *tag.Value
				break
			}
		}
	})
}

func FillInstanceData(nt *db.NodeTable, ec2client *ec2.EC2) error {
	ids := make([]*string, 0, len(nt.Rows))
	for _, row := range nt.Rows {
		if row.AwsID == "" {
			continue
		}
		ids = append(ids, &row.AwsID)
	}

	output, err := ec2client.DescribeInstances(&ec2.DescribeInstancesInput{InstanceIds: ids})
	if err != nil {
		return err
	}
	for _, res := range output.Reservations {
		for _, inst := range res.Instances {
			fillInstance(nt, inst)
		}
	}

	return nil
}

// FillClusterInstanceData adds all instances with the cluster tag,
// which may include additional instances not currently joined to the
// cluster, either still booting up or terminated.
func FillClusterInstanceData(nt *db.NodeTable, ec2client *ec2.EC2) error {
	seen := make(map[string]bool)
	clusters := make([]*string, 0, 0)

	for _, row := range nt.Rows {
		if row.KubernetesCluster == "" {
			continue
		}

		if _, found := seen[row.KubernetesCluster]; found {
			continue
		}

		clusters = append(clusters, &row.KubernetesCluster)
		seen[row.KubernetesCluster] = true
	}

	name := "tag:KubernetesCluster"
	output, err := ec2client.DescribeInstances(&ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			&ec2.Filter{Name: &name, Values: clusters}}})
	if err != nil {
		return err
	}
	for _, res := range output.Reservations {
		for _, inst := range res.Instances {
			fillInstance(nt, inst)
		}
	}

	return nil
}

func FillImageData(nt *db.NodeTable, ec2client *ec2.EC2) error {
	seen := make(map[string]bool)
	imageIDs := make([]*string, 0, 0)

	for _, row := range nt.Rows {
		if row.ImageID == "" {
			continue
		}

		if _, ok := seen[row.ImageID]; ok {
			continue
		}

		imageIDs = append(imageIDs, &row.ImageID)
		seen[row.ImageID] = true
	}

	images, err := ec2client.DescribeImages(&ec2.DescribeImagesInput{ImageIds: imageIDs})
	if err != nil {
		return err
	}

	imagesByID := make(map[string]*ec2.Image)
	for _, image := range images.Images {
		imagesByID[*image.ImageId] = image
	}

	for _, row := range nt.Rows {
		if row.ImageID == "" {
			continue
		}

		image, found := imagesByID[row.ImageID]
		if !found {
			continue
		}

		for _, tag := range image.Tags {
			if *tag.Key == "version" {
				row.ImageVersion = *tag.Value
				break
			}
		}
	}

	return nil
}

type State struct {
	Snapshot db.Snapshot
	Selected string
	Logger   *logger.Logger
}

func (s *State) SelectMove(inc int) {
	index := 0

	if _, i, found := s.Snapshot.NodeTable.ByName(s.Selected); found {
		index = i + inc
	}
	if index >= 0 && index < len(s.Snapshot.NodeTable.Rows) {
		s.Selected = s.Snapshot.NodeTable.Rows[index].Name
	}
}

func (s *State) SelectDown() {
	s.SelectMove(1)
}

func (s *State) SelectUp() {
	s.SelectMove(-1)
}

func LeaderHolderIdentity(clientset *kubernetes.Clientset, name string) (string, error) {
	ep, err := clientset.CoreV1().Endpoints("kube-system").Get(name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("error getting ep/%s", name, err)
	}

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

func Optional(b bool, ifTrue string) string {
	if b {
		return ifTrue
	}
	return ""
}

func Update(state State, buf kit.BufferSlice) {
	topline := make(kit.Line, 0, 0)
	for _, color := range BananaColors {
		topline = append(topline, kit.Cell{Banana, color, 0})
	}
	topline = append(topline, kit.String("press 'q' to quit"))
	topline.Draw(buf.Slice(0, 0, buf.Width, 1))

	padding := kit.Rune(' ')

	header := kit.Row(
		kit.Rune(' '), // master node or not
		kit.Rune(' '), // controller-manager leader
		kit.Rune(' '), // scheduler leader
		padding,
		kit.Rune(' '), // cordoned
		padding,
		kit.String("name"),
		padding,
		kit.Rune('◷'),
		padding,
		kit.String("Pend/Run"), // pending, running
		padding,
		kit.String("version"),
		padding,
		kit.String("aws-id"),
		padding,
		kit.String("image-id"),
		padding,
		kit.String("image-version"),
		padding,
		kit.String("aws-state"))
	header.Bg = termbox.ColorBlack | termbox.AttrBold

	table := kit.Table{Rows: []kit.TableRow{header}}

	counts := state.Snapshot.ClusterPodCounts
	kit.Line{
		// FIXME: pods count
		kit.AttrString{fmt.Sprintf("pods:%d  ", counts.Total), termbox.ColorBlue, 0},
		kit.AttrString{fmt.Sprintf("pending:%d  ", counts.Pending), termbox.ColorYellow, 0},
		kit.AttrString{fmt.Sprintf("running:%d  ", counts.Running), termbox.ColorBlue, 0},
		kit.AttrString{fmt.Sprintf("succeeded:%d  ", counts.Succeeded), termbox.ColorBlue, 0},
		kit.AttrString{fmt.Sprintf("failed:%d  ", counts.Failed), termbox.ColorRed, 0},
	}.Draw(buf.Slice(0, 2, buf.Width, 1))

	if state.Snapshot.NodeTable != nil {
		if len(state.Snapshot.NodeTable.Rows) > 0 {
			kit.Line{
				kit.AttrString{fmt.Sprintf("nodes:%d", state.Snapshot.NodeCount),
					termbox.ColorCyan, 0},
				kit.String(fmt.Sprintf("  cluster:%s", state.Snapshot.ClusterName)),
			}.Draw(buf.Slice(0, 1, buf.Width, 1))
		}

		for _, data := range state.Snapshot.NodeTable.Rows {
			row := kit.Row(
				kit.String(Optional(data.IsMaster, "m")),
				kit.String(Optional(data.IsControllerManagerLeader, "c")),
				kit.String(Optional(data.IsSchedulerLeader, "s")),
				padding,
				kit.String(Optional(data.IsCordoned, "C")),
				padding,
				kit.String(data.Name),
				padding,
				kit.Cell(termbox.Cell{Banana, AgeColor(time.Since(data.CreatedAt)), 0}),
				padding,
				kit.String(fmt.Sprintf("%d/%d", data.PodCounts.Pending, data.PodCounts.Running)),
				padding,
				kit.String(data.KubeletVersion),
				padding,
				kit.String(data.AwsID),
				padding,
				kit.String(data.ImageID),
				padding,
				kit.String(data.ImageVersion),
				padding,
				kit.String(data.AwsState))

			if data.Name == state.Selected {
				row.Fg = termbox.ColorBlack
				row.Bg = termbox.ColorYellow
			}

			table.Rows = append(table.Rows, row)
		}
	}

	table.Draw(buf.Slice(0, 3, buf.Width, buf.Height-3))

	if state.Logger.Len() > 0 {
		kit.AttrString{fmt.Sprintf("%s", state.Logger.At(0).Message), termbox.ColorRed, 0}.Draw(buf.Slice(0, buf.Height-1, buf.Width, 1))
	}
}

func main() {
	defaultKubeconfig := func() string {
		if user, err := user.Current(); err == nil && user.HomeDir != "" {
			return filepath.Join(user.HomeDir, ".kube", "config")
		}
		return ""
	}()
	kubeconfig := flag.String("kubeconfig", defaultKubeconfig, "path to the kube config file")
	kubecontext := flag.String("kubecontext", "", "context within the kube config to use")
	flag.Parse()

	// kubernetes client config
	config, err := clientcmd.NewInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: *kubecontext},
		os.Stdin).ClientConfig()
	if err != nil {
		log.Fatalf("error building kubeconfig: %s: %v", *kubeconfig, err)
	}

	// kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("error creating kubernetes clientset: %v", err)
	}

	// aws client
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	ec2client := ec2.New(sess)

	// TODO: hold more log lines
	logger := logger.New(1)

	// gradually clear log buffer over time
	go func() {
		for {
			time.Sleep(30 * time.Second)
			logger.Infof("")
		}
	}()

	db := db.NewDB(logger, clientset, ec2client)

	state := State{Logger: logger}

	if err := func() error {
		err := termbox.Init()
		if err != nil {
			return err
		}
		defer termbox.Close()

		termbox.SetOutputMode(termbox.Output256)
		termboxEvents := kit.StartPollEvents(context.TODO())

		exitSignal := make(chan os.Signal)
		signal.Notify(exitSignal, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)

		for {
			kit.Update(termbox.ColorWhite, termbox.ColorDefault, func(buf kit.BufferSlice) {
				Update(state, buf)
			})

			select {
			case <-exitSignal:
				return nil
			case ch := <-termboxEvents.Chars:
				if ch == 'q' {
					return nil
				}
			case key := <-termboxEvents.Keys:
				switch key {
				case termbox.KeyArrowDown:
					state.SelectDown()
				case termbox.KeyArrowUp:
					state.SelectUp()
				}
			case <-termboxEvents.Resizes:
				// update ui
			case <-termboxEvents.MouseCoords:
				// ignore
			case err := <-termboxEvents.Errors:
				go func() {
					logger.Warnf("termbox: %v", err)
				}()
			case <-termboxEvents.Interrupts:
				return nil
			case snapshot := <-db.Snapshots:
				state.Snapshot = snapshot
				if state.Selected == "" && len(snapshot.NodeTable.Rows) > 0 {
					state.Selected = snapshot.NodeTable.Rows[0].Name
				}
			case <-logger.Updated:
				// update ui
			}
		}
	}(); err != nil {
		log.Fatalf("error initializing termbox: %v", err)
	}

	log.Printf("goodbye")
}
