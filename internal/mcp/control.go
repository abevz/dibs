package mcp

import (
	"context"
	"strings"

	"github.com/abevz/dibs/internal/core"
)

func (s *Server) changeControl(ctx context.Context, params toolCallParams) (any, error) {
	var args struct {
		core.ControlTarget
		Reason      string `json:"reason"`
		Actor       string `json:"actor"`
		OperationID string `json:"operation_id"`
	}
	if err := unmarshalArgs(params.Arguments, &args); err != nil {
		return nil, err
	}
	actor, err := s.resolveActor(args.Actor, "")
	if err != nil {
		return nil, err
	}
	id, err := toolOperationID(args.OperationID)
	if err != nil {
		return nil, err
	}
	req := core.ControlChangeRequest{
		ControlTarget: args.ControlTarget, Action: strings.TrimPrefix(params.Name, "control_"),
		Reason: args.Reason, Actor: actor, OperationID: id,
	}
	if err := req.Normalize(); err != nil {
		return nil, err
	}
	result, err := s.client.ChangeControl(ctx, req)
	return operationOutcome(id, result, err)
}

func operatorControlTools() []map[string]any {
	common := []schemaField{
		{name: "target_kind", fieldType: "string", description: "issue, repo, worktree, or global.", required: true},
		{name: "target_id", fieldType: "string", description: "Stable ID, issue short ID, repo name or absolute worktree path. Omit for global."},
		{name: "actor", fieldType: "string", description: "Audit label. Defaults to DIBS_ACTOR."},
		operationIDField(),
	}
	restrict := append(append([]schemaField(nil), common...),
		schemaField{name: "reason", fieldType: "string", description: "Fixed code: operator_requested, operator_review, or maintenance."})
	return []map[string]any{
		toolDefinition("control_pause", "Pause covered tool calls while the lease renews. Operator only.", objectSchema(restrict)),
		toolDefinition("control_stop", "Block new tool calls and request a safe handoff. Operator only.", objectSchema(restrict)),
		toolDefinition("control_resume", "Clear the restriction at this target. Operator only.", objectSchema(common)),
	}
}
