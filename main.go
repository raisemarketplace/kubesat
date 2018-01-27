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
	"sort"
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
	NodeTable *NodeTable
}

type Snapshot struct {
	ClusterName      string
	ClusterPodCounts PodCounts
	NodeCount        int
	NodeTable        *NodeTable
}

type NodeTableRow struct {
	IsMaster                  bool
	IsControllerManagerLeader bool
	IsSchedulerLeader         bool
	IsCordoned                bool
	Name                      string
	CreatedAt                 time.Time
	PodCounts                 PodCounts
	PodsPendingCount          int
	PodsRunningCount          int
	PodsSucceededCount        int
	PodsFailedCount           int
	PodsUnknownCount          int
	KubeletVersion            string
	AwsID                     string // k8s external id, also in aws
	ImageID                   string
	ImageVersion              string
	AwsState                  string
	KubernetesCluster         string // aws tag
}

type NodeTable struct {
	Rows       []*NodeTableRow
	nameIndex  map[string]int
	awsIDIndex map[string]int
}

func NewNodeTable() *NodeTable {
	nt := &NodeTable{Rows: make([]*NodeTableRow, 0, 0)}
	nt.reindex()
	return nt
}

type DefaultSort []*NodeTableRow

// Len implements sort.Interface
func (rows DefaultSort) Len() int { return len(rows) }

// Swap implements sort.Interface
func (rows DefaultSort) Swap(i, j int) { rows[i], rows[j] = rows[j], rows[i] }

// Less implements sort.Interface, sorting by role, aws state, name
func (rows DefaultSort) Less(i, j int) bool {
	// masters first
	if rows[i].IsMaster != rows[j].IsMaster {
		return rows[i].IsMaster
	}

	// running first, terminated last
	if rows[i].AwsState != rows[j].AwsState {
		switch rows[i].AwsState {
		case "running":
			return true
		case "terminated":
			return false
		default:
			return rows[i].AwsState < rows[j].AwsState
		}
	}

	// most running pods first
	if rows[i].PodsRunningCount != rows[j].PodsRunningCount {
		return rows[i].PodsRunningCount > rows[j].PodsRunningCount
	}

	// else, by name
	return rows[i].Name < rows[j].Name
}

func (nt *NodeTable) Sort() {
	sort.Sort(DefaultSort(nt.Rows))
	nt.reindex()
}

// Insert appends a row to the table and returns the index of the
// newly inserted node.
func (nt *NodeTable) Insert(row *NodeTableRow) int {
	nt.Rows = append(nt.Rows, row)
	return len(nt.Rows) - 1
}

func (nt *NodeTable) WithRowAtAwsID(id string, f func(*NodeTableRow)) {
	var i int
	var found bool
	var row *NodeTableRow

	if i, found = nt.awsIDIndex[id]; found {
		row = nt.Rows[i]
	} else {
		row = &NodeTableRow{AwsID: id}
		i = nt.Insert(row)
	}

	f(row)
	nt.updateIndexFor(i, row)
}

func (nt *NodeTable) WithRowAtName(name string, f func(*NodeTableRow)) {
	var i int
	var found bool
	var row *NodeTableRow

	if i, found = nt.nameIndex[name]; found {
		row = nt.Rows[i]
	} else {
		row = &NodeTableRow{Name: name}
		i = nt.Insert(row)
	}

	f(row)
	nt.updateIndexFor(i, row)
}

func (nt *NodeTable) ByName(name string) (*NodeTableRow, int, bool) {
	if i, found := nt.nameIndex[name]; found {
		return nt.Rows[i], i, true
	}
	return nil, 0, false
}

func (nt *NodeTable) updateIndexFor(i int, row *NodeTableRow) {
	if row.AwsID != "" {
		nt.awsIDIndex[row.AwsID] = i
	}
	if row.Name != "" {
		nt.nameIndex[row.Name] = i
	}
}

func (nt *NodeTable) reindex() {
	nt.awsIDIndex = make(map[string]int)
	nt.nameIndex = make(map[string]int)

	for i, row := range nt.Rows {
		nt.updateIndexFor(i, row)
	}
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
			return nil, fmt.Errorf("error listing pods in namespace %s: %s", namespace, err)
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
			return nil, fmt.Errorf("error getting %s:ep/%s", name.Namespace, name.Name, err)
		}
		eps[name] = *ep
	}
	return eps, nil
}

func FillPodCounts(nt *NodeTable, clientset *kubernetes.Clientset) error {
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

			nt.WithRowAtName(pod.Spec.NodeName, func(row *NodeTableRow) {
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

func FillNodes(nt *NodeTable, clientset *kubernetes.Clientset) error {
	nodes, err := clientset.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, node := range nodes.Items {
		nt.WithRowAtName(node.Name, func(row *NodeTableRow) {
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
func FillLeaderFlags(nt *NodeTable, clientset *kubernetes.Clientset) error {
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

func fillInstance(nt *NodeTable, inst *ec2.Instance) {
	nt.WithRowAtAwsID(*inst.InstanceId, func(row *NodeTableRow) {
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

func FillInstanceData(nt *NodeTable, ec2client *ec2.EC2) error {
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
func FillClusterInstanceData(nt *NodeTable, ec2client *ec2.EC2) error {
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

func FillImageData(nt *NodeTable, ec2client *ec2.EC2) error {
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

func Join(kubernetesData KubernetesData, awsData AwsData) Snapshot {
	nodeTable := NewNodeTable()

	controllerManagerLeader, err := LeaderHolderIdentity2(kubernetesData.Endpoints[NamespaceName{"kube-system", "kube-controller-manager"}])
	haveControllerManagerLeader := (err == nil)
	schedulerLeader, err := LeaderHolderIdentity2(kubernetesData.Endpoints[NamespaceName{"kube-system", "kube-scheduler"}])
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

		ok := false
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

type State struct {
	Snapshot Snapshot
	Selected string
	Logger   *logger.Logger
}

type NamespaceName struct {
	Namespace string
	Name      string
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

func LeaderHolderIdentity2(ep v1.Endpoints) (string, error) {
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

	db := NewDB(logger, clientset, ec2client)

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
