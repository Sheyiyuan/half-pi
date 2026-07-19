package adminipc

import (
	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
)

type addFaceParams struct {
	Label   string   `json:"label"`
	Scopes  []string `json:"scopes,omitempty"`
	Profile string   `json:"profile,omitempty"`
}

type addLabelParams struct {
	Label string `json:"label"`
}

type removeParams struct {
	ID    int64  `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
}

type emptyParams struct{}

func (s *Server) dispatch(req Request) (any, error) {
	meta := management.RequestMeta{RequestID: req.RequestID, Source: management.SourceIPC}
	switch req.Operation {
	case "status.get":
		if _, err := decodeParams[emptyParams](req.Params); err != nil {
			return nil, err
		}
		return s.service.Status(), nil
	case "peers.list":
		if _, err := decodeParams[emptyParams](req.Params); err != nil {
			return nil, err
		}
		return s.service.Peers()
	case "hand.add":
		params, err := decodeParams[addLabelParams](req.Params)
		if err != nil {
			return nil, err
		}
		return s.service.AddHand(meta, params.Label)
	case "hand.list":
		if _, err := decodeParams[emptyParams](req.Params); err != nil {
			return nil, err
		}
		return s.service.ListHands()
	case "hand.remove":
		params, err := decodeParams[removeParams](req.Params)
		if err != nil {
			return nil, err
		}
		selector, value, err := selector(params.ID, params.Label)
		if err != nil {
			return nil, err
		}
		return s.service.RemoveHand(meta, selector, value)
	case "face.add":
		params, err := decodeParams[addFaceParams](req.Params)
		if err != nil {
			return nil, err
		}
		scopes, err := resolveScopes(params.Profile, params.Scopes)
		if err != nil {
			return nil, err
		}
		return s.service.AddFace(meta, params.Label, scopes)
	case "face.list":
		if _, err := decodeParams[emptyParams](req.Params); err != nil {
			return nil, err
		}
		return s.service.ListFaces()
	case "face.remove":
		params, err := decodeParams[removeParams](req.Params)
		if err != nil {
			return nil, err
		}
		selector, value, err := selector(params.ID, params.Label)
		if err != nil {
			return nil, err
		}
		return s.service.RemoveFace(meta, selector, value)
	default:
		return nil, managementError("invalid_argument", "unknown operation")
	}
}

func selector(id int64, label string) (string, string, error) {
	if id > 0 && label != "" {
		return "", "", managementError("invalid_argument", "--id and --label are mutually exclusive")
	}
	if id > 0 {
		return "id", intString(id), nil
	}
	if label != "" {
		return "label", label, nil
	}
	return "", "", managementError("invalid_argument", "missing --id or --label")
}

func resolveScopes(profile string, raw []string) ([]protocol.FaceScope, error) {
	if profile != "" && len(raw) > 0 {
		return nil, managementError("invalid_argument", "--profile and --scopes are mutually exclusive")
	}
	if profile != "" {
		return management.ExpandProfile(profile)
	}
	scopes := make([]protocol.FaceScope, len(raw))
	for i, scope := range raw {
		scopes[i] = protocol.FaceScope(scope)
	}
	return management.ParseScopes(joinRawScopes(scopes))
}

func joinRawScopes(scopes []protocol.FaceScope) string {
	out := ""
	for i, scope := range scopes {
		if i > 0 {
			out += ","
		}
		out += string(scope)
	}
	return out
}
