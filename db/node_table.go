package db

import (
	"sort"
	"time"
)

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

func (nt *NodeTable) ByAwsID(id string) (*NodeTableRow, int, bool) {
	if i, found := nt.awsIDIndex[id]; found {
		return nt.Rows[i], i, true
	}
	return nil, 0, false
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
