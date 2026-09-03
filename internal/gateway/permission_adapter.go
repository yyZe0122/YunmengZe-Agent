package gateway

import (
	"context"

	"github.com/yyZe0122/yunmengze-agent/internal/toolpermission"
)

// ToolPermissionAdapter adapts toolpermission.Service to ToolPermissionService.
type ToolPermissionAdapter struct {
	Service   *toolpermission.Service
	TrustPath string // ConfigDir permissions-trust.json for allow_permanent
}

func (a ToolPermissionAdapter) ListPending(ctx context.Context, sessionID string, limit int) ([]ToolPermissionView, error) {
	if a.Service == nil {
		return nil, nil
	}
	items, err := a.Service.ListPending(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ToolPermissionView, 0, len(items))
	for _, r := range items {
		view := viewFromRequest(r)
		hint := a.Service.SuggestHabit(ctx, r)
		view.SuggestedDecision = hint.Decision
		view.SuggestedReason = hint.Reason
		out = append(out, view)
	}
	return out, nil
}

func (a ToolPermissionAdapter) Decide(ctx context.Context, permissionID, decision, actor string) (ToolPermissionView, error) {
	return a.DecideWithConfirm(ctx, permissionID, decision, actor, false)
}

func (a ToolPermissionAdapter) DecideWithConfirm(ctx context.Context, permissionID, decision, actor string, confirm bool) (ToolPermissionView, error) {
	if a.Service == nil {
		return ToolPermissionView{}, toolpermission.ErrNotFound
	}
	opts := toolpermission.DecideOptions{Confirm: confirm, TrustPath: a.TrustPath}
	r, err := a.Service.DecideWithOptions(ctx, permissionID, decision, actor, opts)
	if err != nil {
		return ToolPermissionView{}, err
	}
	return viewFromRequest(r), nil
}

func viewFromRequest(r toolpermission.Request) ToolPermissionView {
	return ToolPermissionView{
		ID: r.ID, SessionID: r.SessionID, TaskID: r.TaskID, RunID: r.RunID,
		ToolCallID: r.ToolCallID, ToolName: r.ToolName, Capability: r.Capability,
		Path: r.Path, Command: r.Command, CommandArgs: r.CommandArgs, NetworkDomain: r.NetworkDomain,
		Risk: r.Risk, State: r.State, GrantID: r.GrantID,
		Decision: r.Decision, CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt,
		ExtraRoot: r.ExtraRoot,
	}
}
