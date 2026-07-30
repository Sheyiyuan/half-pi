package facegateway

import (
	"testing"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

func TestSubscriptionDetailModeEnforcesProfile(t *testing.T) {
	fixture := newGatewayFixture(t, 8)
	observer := protocol.FaceIdentity{ID: "observer", Label: "observer", Profile: protocol.FaceProfileObserver}
	state := newGatewayTestConnection(8, observer)
	_, code, _ := fixture.gateway.validateSubscription(state, observer, protocol.FaceSubscribe{
		RequestID: "transparent", DetailMode: protocol.FaceDetailModeTransparent,
	})
	if code != protocol.FaceErrorForbidden {
		t.Fatalf("observer transparent code = %q", code)
	}
	filter, code, _ := fixture.gateway.validateSubscription(state, observer, protocol.FaceSubscribe{RequestID: "summary"})
	if code != "" || filter.detailMode != protocol.FaceDetailModeSummary {
		t.Fatalf("observer default = %q, code=%q", filter.detailMode, code)
	}

	operator := protocol.FaceIdentity{ID: "operator", Label: "operator", Profile: protocol.FaceProfileOperator}
	filter, code, _ = fixture.gateway.validateSubscription(state, operator, protocol.FaceSubscribe{RequestID: "default"})
	if code != "" || filter.detailMode != protocol.FaceDetailModeTransparent {
		t.Fatalf("operator default = %q, code=%q", filter.detailMode, code)
	}
}

func TestSummaryProjectionDropsToolAndApprovalDetails(t *testing.T) {
	args := &protocol.ToolArgsView{ProjectionVersion: "tool-display.v1", Fields: map[string]protocol.ToolFieldView{}, Warnings: []string{}}
	called := projectDomainData(protocol.ChatToolCalledEventData{
		RequestID: "chat", Tool: "tool", ArgsDigest: "sha256:args", Args: args, ProjectionVersion: args.ProjectionVersion,
	}, protocol.FaceDetailModeSummary).(protocol.ChatToolCalledEventData)
	if called.Args != nil || called.ProjectionVersion != "" || called.ArgsDigest == "" {
		t.Fatalf("summary tool projection = %+v", called)
	}
	approval := projectDomainData(protocol.ApprovalRequest{
		ApprovalID: "approval", ArgsDigest: "sha256:args", Args: args, ProjectionVersion: args.ProjectionVersion,
	}, protocol.FaceDetailModeSummary).(protocol.ApprovalRequest)
	if approval.Args != nil || approval.ProjectionVersion != "" || approval.ArgsDigest == "" {
		t.Fatalf("summary approval projection = %+v", approval)
	}
}

func TestTaskLogProjectionRequiresTransparentAdmissionAndConnection(t *testing.T) {
	log := protocol.TaskLogResp{
		TaskID: "task", Offset: 4, NextOffset: 10, Data: []byte("secret"), EOF: true, Truncated: true,
	}
	transparent := projectTaskLog(log, protocol.FaceDetailModeTransparent, protocol.FaceDetailModeTransparent)
	if string(transparent.Data) != "secret" || transparent.DataBytes != 6 || transparent.Digest == "" {
		t.Fatalf("transparent task log = %+v", transparent)
	}
	for _, modes := range [][2]protocol.FaceDetailMode{
		{protocol.FaceDetailModeSummary, protocol.FaceDetailModeTransparent},
		{protocol.FaceDetailModeTransparent, protocol.FaceDetailModeSummary},
	} {
		summary := projectTaskLog(log, modes[0], modes[1])
		if len(summary.Data) != 0 || summary.DataBytes != 6 || summary.NextOffset != log.NextOffset || summary.Digest != transparent.Digest {
			t.Fatalf("summary task log for %v = %+v", modes, summary)
		}
	}
}
