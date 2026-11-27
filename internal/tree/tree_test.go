package tree_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/pdatatest/ptracetest"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/crucibledx/ember/internal/emit"
	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

var start = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func testRun(id string) harness.Run {
	return harness.Run{Harness: "test-harness", SessionID: id, Kind: harness.KindSession,
		Status: harness.StatusCompleted, StartedAt: start, EndedAt: start.Add(time.Minute)}
}
func eq(t *testing.T, got, want any) {
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
func options() tree.Options {
	return tree.Options{Now: start.Add(time.Hour), Idle: time.Minute, Roots: &tree.RootPins{}}
}
func TestIDsMatchPOC(t *testing.T) {
	for name, tc := range map[string][2]string{
		"trace":   {tree.TraceID("workstream/w-1"), "4a959b74cdec38fd4344f794a872b43b"},
		"session": {tree.SessionSpanID("dro"+"id", "s-1"), "af766508c3733a04"},
		"tool":    {tree.ToolSpanID("dro"+"id", "call-1"), "600c8e79f0d6e62e"},
	} {
		t.Run(name, func(t *testing.T) { eq(t, tc[0], tc[1]) })
	}
}
func family() []harness.Run {
	parent := testRun("parent")
	parent.Kind, parent.WorkstreamID = harness.KindOrchestrator, "w"
	parent.ToolCalls = []harness.ToolCall{{ID: "spawn", Name: "spawn", StartedAt: start, EndedAt: start, SpawnedSessionIDs: []string{"agent"}}}
	agent := testRun("agent")
	agent.Kind, agent.WorkstreamID = harness.KindAgent, "w"
	agent.Parent = &harness.ParentRef{SessionID: "parent"}
	agent.ToolCalls = []harness.ToolCall{{ID: "task", Name: "task", StartedAt: start, EndedAt: start}}
	child := testRun("child")
	child.Kind, child.AgentType = harness.KindSubagent, "worker"
	child.Parent = &harness.ParentRef{SessionID: "agent", ToolCallID: "task"}
	return []harness.Run{parent, agent, child}
}
func spansOf(traces ptrace.Traces) map[string]ptrace.Span {
	out := map[string]ptrace.Span{}
	for i := 0; i < traces.ResourceSpans().Len(); i++ {
		spans := traces.ResourceSpans().At(i).ScopeSpans().At(0).Spans()
		for j := 0; j < spans.Len(); j++ {
			span := spans.At(j)
			out[span.SpanID().String()] = span
		}
	}
	return out
}
func TestForest(t *testing.T) {
	t.Run("combined split deterministic and old ancestor", func(t *testing.T) {
		runs := family()
		combined := emit.Traces(tree.BuildForest(runs, options()), emit.Config{})
		split := emit.Traces(tree.BuildForest(runs[1:], options()), emit.Config{})
		emit.Traces(tree.BuildForest(runs[:1], options()), emit.Config{}).ResourceSpans().MoveAndAppendTo(split.ResourceSpans())
		agentID := tree.SessionSpanID("test-harness", "agent")
		eq(t, spansOf(split)[agentID].ParentSpanID().String(), tree.SessionSpanID("test-harness", "parent"))
		spansOf(split)[agentID].SetParentSpanID(spansOf(combined)[agentID].ParentSpanID())
		spansOf(split)[agentID].Attributes().PutStr("ember.parent.tool_call.id", "spawn")
		eq(t, ptracetest.CompareTraces(combined, split), nil)
		eq(t, ptracetest.CompareTraces(combined, emit.Traces(tree.BuildForest(runs, options()), emit.Config{})), nil)
		eq(t, spansOf(combined)[tree.ToolSpanID("test-harness", "task")].ParentSpanID().String(), tree.SessionSpanID("test-harness", "agent"))
		runs[1].StartedAt, runs[1].EndedAt = start.Add(-1000*time.Hour), start.Add(-1000*time.Hour)
		runs[1].ToolCalls[0].StartedAt, runs[1].ToolCalls[0].EndedAt = runs[1].StartedAt, runs[1].EndedAt
		eq(t, tree.BuildForest(runs, options()).Nodes["child"].TraceID(), tree.TraceID("workstream/w"))
		eq(t, emit.MetricDue(runs[1], start, time.Minute, time.Hour, false, false), false)
		eq(t, emit.MetricDue(runs[2], start, time.Minute, time.Hour, false, false), true)
	})
	t.Run("held subtree and pinned descendants", func(t *testing.T) {
		orphan, child, grandchild := testRun("orphan"), testRun("child"), testRun("grandchild")
		orphan.Parent, child.Parent, grandchild.Parent = &harness.ParentRef{SessionID: "missing"}, &harness.ParentRef{SessionID: "orphan"}, &harness.ParentRef{SessionID: "child"}
		state := &tree.RootPins{}
		o := tree.Options{Now: start, Idle: time.Minute, Roots: state}
		held := tree.BuildForest([]harness.Run{orphan, child, grandchild}, o)
		eq(t, len(held.Held), 3)
		eq(t, emit.Traces(held, emit.Config{}).ResourceSpans().Len(), 0)
		eq(t, emit.Logs(held, emit.Config{}).ResourceLogs().Len(), 0)
		eq(t, emit.Metrics(held, emit.Config{}, start).ResourceMetrics().Len(), 0)
		o.Now = start.Add(time.Minute)
		eq(t, tree.BuildForest([]harness.Run{orphan, child, grandchild}, o).Nodes["grandchild"].RootSessionID, "orphan")
		late := tree.BuildForest([]harness.Run{orphan, child, grandchild, testRun("missing")}, o)
		eq(t, late.Nodes["orphan"].ParentSpanID(), "")
		eq(t, late.Nodes["grandchild"].RootSessionID, "orphan")
	})
	t.Run("workstream orphan root and no cross trace parent", func(t *testing.T) {
		agent := family()[1]
		orphan := tree.BuildForest([]harness.Run{agent}, options())
		eq(t, len(orphan.Held), 0)
		eq(t, orphan.Nodes["agent"].RootSessionID, "parent")
		other := family()[2]
		other.WorkstreamID = "other"
		eq(t, spansOf(emit.Traces(tree.BuildForest([]harness.Run{family()[1], other}, options()), emit.Config{}))[tree.SessionSpanID("test-harness", "child")].ParentSpanID().IsEmpty(), true)
	})
	t.Run("attribution and child order", func(t *testing.T) {
		runs := family()
		runs[0].Owner = "owner"
		runs[2].StartedAt, runs[2].EndedAt = start.Add(-time.Hour), start
		runs[2].Status = harness.StatusUnknown
		sibling := testRun("sibling")
		sibling.Parent = &harness.ParentRef{SessionID: "parent"}
		sibling.StartedAt = start.Add(-2 * time.Hour)
		runs = append(runs, sibling)
		o := tree.Options{Now: start.Add(time.Hour), Idle: 2 * time.Hour, Roots: &tree.RootPins{}}
		o.Attribution = []tree.AttributionRecord{{At: start.Add(30 * time.Minute), Human: "human", Source: "source", SessionID: "child", Role: "active"}}
		forest := tree.BuildForest(runs, o)
		eq(t, forest.Nodes["child"].StartedBy, "test-harness:owner")
		eq(t, forest.Nodes["parent"].Active, []string{"source:human", "test-harness:owner"})
		eq(t, forest.Nodes["agent"].Children[0].Run.SessionID, "child")
		eq(t, forest.Nodes["parent"].Children[0].Run.SessionID, "sibling")
	})
}
func TestSegments(t *testing.T) {
	run := testRun("segments")
	run.Model, run.WorkstreamID = "model", "w"
	run.Parent = &harness.ParentRef{SessionID: "parent", ToolCallID: "task"}
	run.StartedAt = start
	run.TurnTimes = []time.Time{start, start.Add(2*time.Minute + time.Nanosecond)}
	run.ToolCalls = []harness.ToolCall{{ID: "call", Name: "read", StartedAt: run.TurnTimes[1], EndedAt: run.TurnTimes[1]}}
	run.EndedAt = run.TurnTimes[1]
	segments := tree.Segments(run, time.Minute, start.Add(time.Hour))
	eq(t, len(segments), 2)
	eq(t, segments, tree.Segments(run, time.Minute, start.Add(time.Hour)))
	for _, tc := range []struct {
		at   time.Time
		want int
	}{
		{start.Add(-time.Second), 1}, {start.Add(time.Minute), 1}, {start.Add(time.Minute + time.Nanosecond), 2},
		{run.EndedAt.Add(time.Minute), 2}, {run.EndedAt.Add(time.Minute + time.Nanosecond), 3},
	} {
		got, ok := tree.SegmentAt(segments, tc.at, time.Minute)
		eq(t, ok, true)
		eq(t, got.Number, tc.want)
	}
	_, ok := tree.SegmentAt(nil, start, time.Minute)
	eq(t, ok, false)
	o := options()
	o.Attribution = []tree.AttributionRecord{{At: start, Human: "starter", Source: "s", SessionID: "segments", Role: "started"}, {At: run.EndedAt, Human: "active", Source: "s", SessionID: "segments", Role: "active"}}
	traces := emit.Traces(tree.BuildForest([]harness.Run{run}, o), emit.Config{})
	spans := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
	second := spans.At(1)
	eq(t, second.ParentSpanID().String(), tree.SessionSpanID("test-harness", "segments"))
	eq(t, spans.At(2).ParentSpanID().String(), second.SpanID().String())
	eq(t, second.Attributes().AsRaw(), map[string]any{
		"session.id": "segments", "gen_ai.operation.name": "invoke_agent", "gen_ai.conversation.id": "segments", "gen_ai.agent.name": "session", "gen_ai.request.model": "model",
		"ember.harness": "test-harness", "ember.agent.kind": "session", "ember.workstream.id": "w", "ember.work.id": "w",
		"ember.parent.session.id": "parent", "ember.parent.tool_call.id": "task", "ember.human.started_by": "s:starter", "ember.human.active": "s:active,s:starter",
		"ember.session.segment": int64(2), "ember.agent.llm_turns": int64(1), "ember.agent.tool_calls": int64(1),
	})
}

type fakeGit struct {
	files map[string]string
	dirs  map[string]bool
}

func (g fakeGit) ReadFile(name string) ([]byte, error) {
	if value, ok := g.files[name]; ok {
		return []byte(value), nil
	}
	return nil, errors.New("missing")
}
func (g fakeGit) IsDir(name string) bool { return g.dirs[name] }
func TestWorkID(t *testing.T) {
	issue := tree.BranchPattern{Match: regexp.MustCompile(`(?:^|/)([A-Z]+-\d+)`), ID: "$1"}
	t.Run("overrides and inheritance", func(t *testing.T) {
		runs := family()
		runs[0].Branch, runs[1].Branch = "feature/ABC-12", "feature/DEF-34"
		o := options()
		o.Work = tree.WorkIDOptions{Explicit: map[string]string{"child": "explicit"}, Patterns: []tree.BranchPattern{issue}}
		forest := tree.BuildForest(runs, o)
		eq(t, forest.Nodes["parent"].WorkID, "ABC-12")
		eq(t, forest.Nodes["agent"].WorkID, "DEF-34")
		eq(t, forest.Nodes["child"].WorkID, "explicit")
		runs[1].Branch, runs[2].Parent = "", nil
		o.Work.Explicit = nil
		forest = tree.BuildForest(runs, o)
		eq(t, forest.Nodes["agent"].WorkID, "ABC-12")
		runs[0].Branch = ""
		forest = tree.BuildForest(runs, o)
		eq(t, forest.Nodes["agent"].WorkID, "w")
		runs[0].WorkstreamID, runs[1].WorkstreamID = "", ""
		forest = tree.BuildForest(runs, o)
		eq(t, forest.Nodes["agent"].WorkID, "parent")
	})
	t.Run("portable worktree and origin selection", func(t *testing.T) {
		root, git := filepath.Join(string(filepath.Separator), "repo"), filepath.Join(string(filepath.Separator), "git")
		worktree, metadata := filepath.Join(root, "worktree"), filepath.Join(git, "worktrees", "w")
		run := testRun("git")
		run.Cwd = filepath.Join(worktree, "pkg", "subdir")
		files := map[string]string{
			filepath.Join(worktree, ".git"): "gitdir: " + filepath.Join("..", "..", "git", "worktrees", "w") + "\n",
			filepath.Join(metadata, "HEAD"): "ref: refs/heads/loop/9-processor\n", filepath.Join(metadata, "commondir"): filepath.Join("..", "..") + "\n",
			filepath.Join(git, "config"): "[remote \"origin\"]\nurl = https://github.com/crucibledx/ember.git\n[remote \"other\"]\nurl = git@github.com:wrong/repo.git\n",
		}
		p := tree.BranchPattern{Match: regexp.MustCompile(`loop/(\d+)-`), ID: "{repo}#$1"}
		o := options()
		o.Work = tree.WorkIDOptions{Git: fakeGit{files: files, dirs: map[string]bool{}}, Patterns: []tree.BranchPattern{p}}
		eq(t, tree.BuildForest([]harness.Run{run}, o).Nodes["git"].WorkID, "crucibledx/ember#9")
		files[filepath.Join(git, "config")] = "[remote \"other\"]\nurl = git@github.com:wrong/repo.git\n[remote \"origin\"]\nurl=git@github.com:crucibledx/ember.git\n"
		eq(t, tree.BuildForest([]harness.Run{run}, o).Nodes["git"].WorkID, "crucibledx/ember#9")
		delete(files, filepath.Join(git, "config"))
		run.WorkstreamID = "fallback"
		eq(t, tree.BuildForest([]harness.Run{run}, o).Nodes["git"].WorkID, "fallback")
		run.WorkstreamID = ""
		eq(t, tree.BuildForest([]harness.Run{run}, o).Nodes["git"].WorkID, "git")
		o.Work.Git = nil
		eq(t, tree.BuildForest([]harness.Run{run}, o).Nodes["git"].WorkID, "git")
	})
}
