package toolpermission

import (
	"context"
	"fmt"
	"time"

	"github.com/yyZe0122/yunmengze-agent/internal/tools"
)

// Gate adapts Service to tools.PermissionGate.
type Gate struct {
	service *Service
}

func NewGate(service *Service) *Gate {
	if service == nil {
		return nil
	}
	return &Gate{service: service}
}

// CreatePending implements tools.PermissionGate.
func (g *Gate) CreatePending(ctx context.Context, req tools.PermissionPending) (string, error) {
	if g == nil || g.service == nil || g.service.store == nil {
		return "", fmt.Errorf("permission gate unavailable")
	}
	id := "perm-" + shortHash(req.RunID, req.ToolCallID, req.ToolName, time.Now().UTC().Format(time.RFC3339Nano))
	g.service.waiter.Register(id)
	now := time.Now().UTC()
	if g.service.now != nil {
		now = g.service.now().UTC()
	}
	expires := now.Add(WaitTimeout).Format(time.RFC3339Nano)
	row := Request{
		ID: id, SessionID: req.SessionID, TaskID: req.TaskID, RunID: req.RunID,
		PlanID: req.PlanID, PlanHash: req.PlanHash, StepID: req.StepID,
		ToolCallID: req.ToolCallID, ToolName: req.ToolName, Arguments: req.Arguments,
		Capability: req.Capability, Path: req.Path, Command: req.Command,
		CommandArgs: req.CommandArgs, NetworkDomain: req.NetworkDomain, Risk: req.Risk,
		State: StatePending, CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: expires,
		ExtraRoot: req.ExtraRoot,
	}
	err := g.service.store.Insert(ctx, row)
	if err != nil {
		return "", err
	}
	g.service.EmitPending(ctx, row)
	return id, nil
}

// Wait implements tools.PermissionGate.
func (g *Gate) Wait(ctx context.Context, permissionID string) (tools.PermissionDecision, error) {
	if g == nil || g.service == nil {
		return tools.PermissionDecision{}, context.Canceled
	}
	if req, err := g.service.store.Get(ctx, permissionID); err == nil && req.State != StatePending {
		return tools.PermissionDecision{
			Decision: req.Decision, GrantID: req.GrantID, State: req.State,
		}, nil
	}
	d, err := g.service.waiter.Wait(ctx, permissionID)
	if err != nil {
		return tools.PermissionDecision{}, err
	}
	return tools.PermissionDecision{
		Decision: d.Decision, GrantID: d.GrantID, State: d.State,
	}, nil
}

var _ tools.PermissionGate = (*Gate)(nil)
