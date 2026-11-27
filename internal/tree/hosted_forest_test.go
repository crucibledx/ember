package tree_test

import (
	"testing"
	"time"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

func TestForestDoesNotAttachParentFromAnotherHarness(t *testing.T) {
	otherParent := testRun("shared-parent")
	otherParent.Harness = "other"
	child := testRun("child")
	child.Harness = "intended"
	child.Parent = &harness.ParentRef{SessionID: otherParent.SessionID}

	forest := tree.BuildForest([]harness.Run{otherParent, child}, tree.Options{
		Now:   start,
		Idle:  time.Minute,
		Roots: &tree.RootPins{},
	})

	got := forest.Nodes["child"]
	if !got.Held {
		t.Fatal("child with a missing same-harness parent was not held")
	}
	if len(forest.Nodes["shared-parent"].Children) != 0 {
		t.Fatal("other-harness parent gained a child")
	}
}
