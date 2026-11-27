package tree

import (
	"cmp"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crucibledx/ember/internal/harness"
)

type RootState interface {
	RootKey(harnessName, runID string) (string, bool)
	PinRoot(harnessName, runID, rootKey string)
	MissingSince(harnessName, runID string, now time.Time) time.Time
}
type RootPins struct {
	Roots   map[string]string
	Missing map[string]time.Time
}

func RootRunID(harnessName, runID string) string {
	return strconv.Itoa(len(harnessName)) + ":" + harnessName + runID
}
func (p *RootPins) RootKey(harnessName, runID string) (string, bool) {
	key, ok := p.Roots[RootRunID(harnessName, runID)]
	return key, ok
}
func (p *RootPins) PinRoot(harnessName, runID, rootKey string) {
	if p.Roots == nil {
		p.Roots = map[string]string{}
	}
	p.Roots[RootRunID(harnessName, runID)] = rootKey
}
func (p *RootPins) MissingSince(harnessName, runID string, now time.Time) time.Time {
	if p.Missing == nil {
		p.Missing = map[string]time.Time{}
	}
	key := RootRunID(harnessName, runID)
	if at, ok := p.Missing[key]; ok {
		return at
	}
	p.Missing[key] = now
	return now
}

type Options struct {
	Now          time.Time
	Idle         time.Duration
	Roots        RootState
	Work         WorkIDOptions
	Attribution  []AttributionRecord
	DefaultHuman string
}
type Node struct {
	Run           harness.Run
	Parent        *harness.ParentRef
	Children      []*Node
	WorkstreamID  string
	RootKey       string
	RootSessionID string
	WorkID        string
	StartedBy     string
	Active        []string
	Held          bool
	Detached      bool
	Segments      []Segment
	Idle          time.Duration
	parent        *Node
}
type Forest struct {
	Roots []*Node
	Nodes map[string]*Node
	Held  []*Node
}

func BuildForest(runs []harness.Run, options Options) *Forest {
	if options.Roots == nil {
		options.Roots = &RootPins{}
	}
	forest := &Forest{Nodes: map[string]*Node{}}
	ordered := append([]harness.Run(nil), runs...)
	sort.Slice(ordered, func(i, j int) bool {
		return cmp.Or(strings.Compare(ordered[i].SessionID, ordered[j].SessionID), strings.Compare(ordered[i].Harness, ordered[j].Harness)) < 0
	})
	for _, run := range ordered {
		key := run.SessionID
		if existing := forest.Nodes[key]; existing != nil && existing.Run.Harness != run.Harness {
			delete(forest.Nodes, key)
			forest.Nodes[existing.Run.Harness+"/"+existing.Run.SessionID] = existing
			key = run.Harness + "/" + run.SessionID
		}
		if _, exists := forest.Nodes[key]; !exists {
			forest.Nodes[key] = &Node{Run: run}
		}
	}
	for _, node := range forest.Ordered() {
		linkParent(forest, node, options.Roots)
	}
	for _, node := range forest.Ordered() {
		if node.Parent != nil && node.parent == nil {
			resolveOrphan(forest, node, options)
		}
		if (node.Parent == nil || node.parent == nil) && !node.Held {
			forest.Roots = append(forest.Roots, node)
		}
	}
	for _, node := range forest.Held {
		holdDescendants(node, forest)
	}
	for _, root := range forest.Roots {
		rootSession := cmp.Or(root.RootSessionID, root.Run.SessionID)
		assignLineage(root, root.RootKey, rootSession, "", root.Detached, options)
	}
	return forest
}
func linkParent(forest *Forest, node *Node, roots RootState) {
	ref := node.Run.Parent
	if ref == nil || ref.SessionID == node.Run.SessionID {
		return
	}
	copyRef := *ref
	node.Parent = &copyRef
	parent := forest.Nodes[node.Run.Harness+"/"+ref.SessionID]
	if parent == nil {
		parent = forest.Nodes[ref.SessionID]
	}
	if parent != nil {
		if key, ok := roots.RootKey(node.Run.Harness, node.Run.SessionID); ok {
			node.RootKey, node.Detached = key, true
			return
		}
		node.parent = parent
		if node.Parent.ToolCallID == "" {
			node.Parent.ToolCallID = spawningCall(parent.Run, node.Run.SessionID)
		}
		parent.Children = append(parent.Children, node)
	}
}
func spawningCall(parent harness.Run, childID string) string {
	for _, call := range parent.ToolCalls {
		if slices.Contains(call.SpawnedSessionIDs, childID) {
			return call.ID
		}
	}
	return ""
}
func resolveOrphan(forest *Forest, node *Node, options Options) {
	if key, ok := options.Roots.RootKey(node.Run.Harness, node.Run.SessionID); ok {
		node.RootKey, node.Detached = key, true
		return
	}
	if node.Run.WorkstreamID != "" {
		node.RootKey = WorkstreamRootKey(node.Run.WorkstreamID)
		node.RootSessionID = node.Parent.SessionID
		return
	}
	since := options.Roots.MissingSince(node.Run.Harness, node.Run.SessionID, options.Now)
	if options.Now.Before(since.Add(options.Idle)) {
		node.Held = true
		forest.Held = append(forest.Held, node)
		return
	}
	node.RootKey = SessionRootKey(node.Run.Harness, node.Run.SessionID)
	node.Detached = true
	options.Roots.PinRoot(node.Run.Harness, node.Run.SessionID, node.RootKey)
}
func assignLineage(node *Node, root, rootSession, inheritedWS string, preserveRoot bool, options Options) {
	if root == "" {
		root = SessionRootKey(node.Run.Harness, rootSession)
	}
	node.RootKey, node.RootSessionID = root, rootSession
	node.WorkstreamID = node.Run.WorkstreamID
	if node.WorkstreamID == "" {
		node.WorkstreamID = inheritedWS
	}
	if node.WorkstreamID != "" && !preserveRoot {
		node.RootKey = WorkstreamRootKey(node.WorkstreamID)
	}
	if pinned, ok := options.Roots.RootKey(node.Run.Harness, node.Run.SessionID); ok {
		node.RootKey, node.RootSessionID, node.Detached = pinned, node.Run.SessionID, true
		preserveRoot = true
	}
	node.Segments = Segments(node.Run, options.Idle, options.Now)
	node.Idle = options.Idle
	node.WorkID = ownWorkID(node.Run, options.Work)
	if node.WorkID == "" && node.parent != nil {
		node.WorkID = node.parent.WorkID
	}
	if node.WorkID == "" {
		node.WorkID = node.WorkstreamID
	}
	if node.WorkID == "" {
		node.WorkID = rootSession
	}
	applyAttribution(node, options.Attribution, options.DefaultHuman, options.Now)
	slices.SortFunc(node.Children, func(a, b *Node) int {
		return cmp.Or(a.Run.StartedAt.Compare(b.Run.StartedAt), strings.Compare(a.Run.SessionID, b.Run.SessionID))
	})
	for _, child := range node.Children {
		assignLineage(child, node.RootKey, node.RootSessionID, node.WorkstreamID, preserveRoot, options)
	}
}
func holdDescendants(node *Node, forest *Forest) {
	for _, child := range node.Children {
		if child.Held {
			continue
		}
		child.Held = true
		forest.Held = append(forest.Held, child)
		holdDescendants(child, forest)
	}
}
func (n *Node) TraceID() string { return TraceID(n.RootKey) }
func (n *Node) ParentSpanID() string {
	if n.Parent == nil || n.Held || n.Detached {
		return ""
	}
	if n.parent == nil {
		return SessionSpanID(n.Run.Harness, n.Parent.SessionID)
	}
	if n.TraceID() != n.parent.TraceID() {
		return ""
	}
	if n.Parent.ToolCallID != "" {
		return ToolSpanID(n.Run.Harness, n.Parent.ToolCallID)
	}
	return SessionSpanID(n.Run.Harness, n.Parent.SessionID)
}
func (f *Forest) Ordered() []*Node {
	return slices.SortedFunc(maps.Values(f.Nodes), func(a, b *Node) int {
		return strings.Compare(a.Run.SessionID, b.Run.SessionID)
	})
}
