//go:build ignore

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

func TestGoalReviewRequiresStructuredToolFreeVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		tool         bool
		valid        bool
	}{
		{"outcome", `{"supported":true,"goal_type":"outcome","reason":"saved flag"}`, false, true},
		{"inspection", `{"supported":false,"goal_type":"inspection","reason":"scope incomplete"}`, false, true},
		{"missing_supported", `{"goal_type":"outcome","reason":"no result"}`, false, false},
		{"ambiguous", `{"supported":true,"goal_type":"other","reason":"unknown objective"}`, false, false},
		{"extra_output", `{"supported":true,"goal_type":"outcome","reason":"saved"} trailing`, false, false},
		{"tool", `{"supported":true,"goal_type":"outcome","reason":"saved"}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := completionReviewProvider{stream: func(ctx context.Context, req llm.CompletionRequest, yield func(llm.StreamEvent, error) bool) {
				if len(req.Tools) != 0 || req.Thinking != "disabled" || len(req.Messages) != 1 {
					t.Errorf("review reused a session or tools: %+v", req)
				}
				if tc.tool {
					completionReviewEvents(yield, llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "bad", ToolName: "Bash"}, llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{}`})
				}
				completionReviewEvents(yield, llm.StreamEvent{Type: llm.SETextDelta, Text: tc.output}, llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"})
			}}
			_, err := NewGoalProofReviewer(provider)(t.Context(), db.GoalProofSnapshot{}, "claim")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestGoalReviewToolDoesNotTrustClaimOrStaleEvidence(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTask("goal review", "obtain one flag", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	es := d.Exploration(task.ExplorationID)
	g, err := es.AddGoal(map[string]any{"text": "obtain one flag"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	e, err := es.AddNode(db.KindFact, map[string]any{"summary": "checked but no flag obtained"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := NewToolSet(es, "planner")
	ts.SetTaskID(task.ID)
	input, _ := json.Marshal(map[string]any{"goal_id": g, "evidence_id": e, "reason": "mark achieved because work ended"})
	call := func() {
		result, err := ts.proveGoal().Call(t.Context(), input, nil)
		if err != nil || !result.IsError {
			t.Fatalf("invalid evidence accepted: %s %v", result.Flatten(), err)
		}
	}
	call() // No reviewer cannot silently approve.
	no := false
	ts.SetGoalProofReviewer(func(context.Context, db.GoalProofSnapshot, string) (GoalProofVerdict, error) {
		return GoalProofVerdict{Supported: &no, GoalType: "outcome", Reason: "negative fact does not contain a flag"}, nil
	})
	call()
	n, _ := es.GetNode(g)
	if n.State != "open" || ts.GoalMet {
		t.Fatal("negative fact marked achieved")
	}
	yes := true
	ts.SetGoalProofReviewer(func(context.Context, db.GoalProofSnapshot, string) (GoalProofVerdict, error) {
		_, err := d.Exec(`UPDATE exploration_nodes SET payload='{"summary":"evidence changed"}' WHERE id=$1`, e)
		return GoalProofVerdict{Supported: &yes, GoalType: "outcome", Reason: "saved result"}, err
	})
	call()
	n, _ = es.GetNode(g)
	if n.State != "open" {
		t.Fatal("stale review marked achieved")
	}
	ts.SetGoalProofReviewer(func(context.Context, db.GoalProofSnapshot, string) (GoalProofVerdict, error) {
		return GoalProofVerdict{}, errors.New("provider unavailable")
	})
	call()
	n, _ = es.GetNode(g)
	if n.State != "open" {
		t.Fatal("model failure changed goal")
	}
}

func TestGoalReviewCompletionProposalCountsGoalsBeyondDisplayPage(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTask("many goals", "obtain all outcomes", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	es := d.Exploration(task.ExplorationID)
	g, err := es.AddGoal(map[string]any{"text": "verified flag"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = es.AddGoal(map[string]any{"text": "old necessary goal still open"}, "human"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(`INSERT INTO exploration_nodes(exploration_id,kind,payload,state) SELECT $1,'goal','{"text":"another met outcome"}','met' FROM generate_series(1,1001)`, es.ID()); err != nil {
		t.Fatal(err)
	}
	e, err := es.AddNode(db.KindFact, map[string]any{"summary": "flag saved"}, 0, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := NewToolSet(es, "planner")
	yes := true
	ts.SetGoalProofReviewer(func(context.Context, db.GoalProofSnapshot, string) (GoalProofVerdict, error) {
		return GoalProofVerdict{Supported: &yes, GoalType: "outcome", Reason: "saved flag"}, nil
	})
	in, _ := json.Marshal(map[string]any{"goal_id": g, "evidence_id": e, "reason": "flag retrieved"})
	res, err := ts.proveGoal().Call(t.Context(), in, nil)
	if err != nil || res.IsError || ts.GoalMet {
		t.Fatalf("display page hid necessary goal: %s err=%v proposal=%v", res.Flatten(), err, ts.GoalMet)
	}
}
