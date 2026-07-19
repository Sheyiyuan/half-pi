package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/mattn/go-isatty"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/adminipc"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/config"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/management"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/setup"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/statelock"
	"github.com/Sheyiyuan/half-pi/modules/half-pi-mind/internal/store"
)

type cliError struct {
	code string
	exit int
	msg  string
}

func (e *cliError) Error() string { return e.msg }

func isManagementCommand(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return false
	}
	switch args[0] {
	case "config", "face", "hand", "status", "peers":
		return true
	default:
		return false
	}
}

func runCLI(args []string, output, logs io.Writer) error {
	if len(args) == 0 {
		return usage("missing command")
	}
	switch args[0] {
	case "config":
		return runConfigCLI(args[1:], output)
	case "face":
		return runCredentialCLI("face", args[1:], output, logs)
	case "hand":
		return runCredentialCLI("hand", args[1:], output, logs)
	case "status":
		return runStatusCLI(args[1:], output)
	case "peers":
		return runPeersCLI(args[1:], output)
	default:
		return usage("unknown command %q", args[0])
	}
}

func runConfigCLI(args []string, output io.Writer) error {
	if len(args) == 0 {
		return usage("missing config command")
	}
	switch args[0] {
	case "init":
		if len(args) != 1 {
			return usage("config init does not accept arguments")
		}
		env, err := setup.Init()
		if err != nil {
			return cliWrap("internal", 1, "config init", err)
		}
		fmt.Fprintf(output, "initialized: %s\n", env.Config)
		return nil
	case "path":
		if len(args) != 1 {
			return usage("config path does not accept arguments")
		}
		env, err := setup.Resolve()
		if err != nil {
			return cliWrap("internal", 1, "resolve paths", err)
		}
		fmt.Fprintf(output, "home:             %s\n", env.HomeDir)
		fmt.Fprintf(output, "config:           %s\n", env.Config)
		fmt.Fprintf(output, "database:         %s\n", env.DBPath)
		fmt.Fprintf(output, "logs:             %s\n", env.LogDir)
		fmt.Fprintf(output, "run:              %s\n", env.RunDir)
		fmt.Fprintf(output, "lock:             %s\n", env.LockPath)
		fmt.Fprintf(output, "control_endpoint: %s\n", env.ControlEndpoint)
		return nil
	case "validate":
		if len(args) != 1 {
			return usage("config validate does not accept arguments")
		}
		env, err := setup.Resolve()
		if err != nil {
			return cliWrap("internal", 1, "resolve paths", err)
		}
		cfg, err := config.Load(env.Config)
		if err != nil {
			return cliWrap("invalid_config", 3, "load config", err)
		}
		if err := management.ValidateConfig(cfg); err != nil {
			return cliFromManagement(err)
		}
		fmt.Fprintln(output, "config valid")
		return nil
	case "show":
		fs := flag.NewFlagSet("config show", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		format := fs.String("format", "toml", "toml|json")
		if err := fs.Parse(args[1:]); err != nil {
			return usage("%v", err)
		}
		if fs.NArg() != 0 {
			return usage("config show does not accept positional arguments")
		}
		env, err := setup.Resolve()
		if err != nil {
			return cliWrap("internal", 1, "resolve paths", err)
		}
		cfg, err := config.Load(env.Config)
		if err != nil {
			return cliWrap("invalid_config", 3, "load config", err)
		}
		return writeConfig(output, cfg.Sanitized(), *format)
	default:
		return usage("unknown config command %q", args[0])
	}
}

func runCredentialCLI(kind string, args []string, output, prompt io.Writer) error {
	if len(args) == 0 {
		return usage("missing %s command", kind)
	}
	switch args[0] {
	case "add":
		return runCredentialAdd(kind, args[1:], output)
	case "list":
		return runCredentialList(kind, args[1:], output)
	case "remove":
		return runCredentialRemove(kind, args[1:], output, os.Stdin, prompt, isInteractiveTerminal(os.Stdin))
	default:
		return usage("unknown %s command %q", kind, args[0])
	}
}

func runCredentialAdd(kind string, args []string, output io.Writer) error {
	if len(args) == 0 {
		return usage("%s add requires a label", kind)
	}
	label := args[0]
	fs := flag.NewFlagSet(kind+" add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "text", "text|json|toml")
	scopesText := fs.String("scopes", "", "comma-separated Face scopes")
	profile := fs.String("profile", "", "observer|operator")
	if err := fs.Parse(args[1:]); err != nil {
		return usage("%v", err)
	}
	if fs.NArg() != 0 {
		return usage("%s add does not accept extra positional arguments", kind)
	}
	if err := validateCredentialAddFormat(*format); err != nil {
		return err
	}
	if kind == "hand" && (*scopesText != "" || *profile != "") {
		return usage("hand add does not accept Face scopes or profile")
	}
	var params any
	if kind == "face" {
		if (*scopesText == "") == (*profile == "") {
			return usage("face add requires exactly one of --scopes or --profile")
		}
		var scopes []string
		if *scopesText != "" {
			parsed, err := management.ParseScopes(*scopesText)
			if err != nil {
				return cliFromManagement(err)
			}
			scopes = scopeStrings(parsed)
		}
		params = map[string]any{"label": label, "scopes": scopes, "profile": *profile}
	} else {
		params = map[string]string{"label": label}
	}
	result, err := callManaged(kind+".add", params, func(svc *management.Service, meta management.RequestMeta) (any, error) {
		if kind == "hand" {
			return svc.AddHand(meta, label)
		}
		scopes, err := faceScopesForCLI(*profile, *scopesText)
		if err != nil {
			return nil, err
		}
		return svc.AddFace(meta, label, scopes)
	})
	if err != nil {
		return err
	}
	var credential management.SecretCredentialDTO
	if err := decodeResult(result, &credential); err != nil {
		return cliWrap("internal", 1, "decode result", err)
	}
	return writeCredentialAdd(output, kind, credential, *format)
}

func runCredentialList(kind string, args []string, output io.Writer) error {
	fs := flag.NewFlagSet(kind+" list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table|json")
	if err := fs.Parse(args); err != nil {
		return usage("%v", err)
	}
	if fs.NArg() != 0 {
		return usage("%s list does not accept positional arguments", kind)
	}
	if *format != "table" && *format != "json" {
		return usage("unsupported format %q", *format)
	}
	result, err := callManaged(kind+".list", map[string]any{}, func(svc *management.Service, meta management.RequestMeta) (any, error) {
		if kind == "hand" {
			return svc.ListHands()
		}
		return svc.ListFaces()
	})
	if err != nil {
		return err
	}
	var credentials []management.CredentialDTO
	if err := decodeResult(result, &credentials); err != nil {
		return cliWrap("internal", 1, "decode result", err)
	}
	if *format == "json" {
		return writeJSONOK(output, credentials)
	}
	writeCredentialTable(output, kind, credentials)
	return nil
}

func runCredentialRemove(kind string, args []string, output io.Writer, input io.Reader, prompt io.Writer, interactive bool) error {
	fs := flag.NewFlagSet(kind+" remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "text", "text|json")
	id := fs.Int64("id", 0, "credential id")
	label := fs.String("label", "", "credential label")
	yes := fs.Bool("yes", false, "confirm removal")
	if err := fs.Parse(args); err != nil {
		return usage("%v", err)
	}
	if fs.NArg() != 0 {
		return usage("%s remove does not accept positional arguments", kind)
	}
	if *format != "text" && *format != "json" {
		return usage("unsupported format %q", *format)
	}
	selector, value, err := removeSelector(*id, *label)
	if err != nil {
		return cliFromManagement(err)
	}
	if !*yes {
		if !interactive {
			return usage("%s remove requires --yes outside an interactive terminal", kind)
		}
		if err := confirmRemoval(input, prompt, kind, *id, *label); err != nil {
			return err
		}
	}
	params := map[string]any{}
	if *id > 0 {
		params["id"] = *id
	}
	if *label != "" {
		params["label"] = *label
	}
	result, err := callManaged(kind+".remove", params, func(svc *management.Service, meta management.RequestMeta) (any, error) {
		if kind == "hand" {
			return svc.RemoveHand(meta, selector, value)
		}
		return svc.RemoveFace(meta, selector, value)
	})
	if err != nil {
		return err
	}
	var removed management.RemoveResult
	if err := decodeResult(result, &removed); err != nil {
		return cliWrap("internal", 1, "decode result", err)
	}
	if *format == "json" {
		return writeJSONOK(output, removed)
	}
	fmt.Fprintf(output, "%s credential %q removed", kind, removed.Label)
	if removed.Disconnected {
		fmt.Fprint(output, " and disconnected")
	}
	fmt.Fprintln(output)
	return nil
}

func runStatusCLI(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "text", "text|json")
	if err := fs.Parse(args); err != nil {
		return usage("%v", err)
	}
	if fs.NArg() != 0 {
		return usage("status does not accept positional arguments")
	}
	if *format != "text" && *format != "json" {
		return usage("unsupported format %q", *format)
	}
	env, err := setup.Resolve()
	if err != nil {
		return cliWrap("internal", 1, "resolve paths", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := adminipc.Call(ctx, env.ControlEndpoint, adminipc.Request{
		Version: adminipc.Version, RequestID: uuid.NewString(), Operation: "status.get",
	})
	if err == nil {
		if !resp.OK {
			return cliIPCError(resp.Error)
		}
		var status management.StatusResult
		if err := decodeResult(resp.Result, &status); err != nil {
			return cliWrap("internal", 1, "decode status", err)
		}
		return writeStatus(output, status, *format)
	}
	var unavailable *adminipc.UnavailableError
	if !errors.As(err, &unavailable) {
		return &cliError{code: "control_unavailable", exit: 5, msg: fmt.Sprintf("management IPC protocol failed: %v", err)}
	}
	if !unavailable.Fallback {
		if writeErr := writeStatus(output, management.StatusResult{State: "degraded"}, *format); writeErr != nil {
			return writeErr
		}
		return &cliError{code: "control_unavailable", exit: 5, msg: fmt.Sprintf("management IPC is unavailable: %v", unavailable)}
	}
	lock, lockErr := statelock.Acquire(ctx, env.LockPath, statelock.Info{Mode: "status"})
	if lockErr == nil {
		_ = lock.Close()
		return writeStatus(output, management.StatusResult{State: "stopped"}, *format)
	}
	if writeErr := writeStatus(output, management.StatusResult{State: "degraded"}, *format); writeErr != nil {
		return writeErr
	}
	return &cliError{code: "state_busy", exit: 5, msg: "Mind state is locked but management IPC is unavailable"}
}

func runPeersCLI(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("peers", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "text", "text|json")
	if err := fs.Parse(args); err != nil {
		return usage("%v", err)
	}
	if fs.NArg() != 0 {
		return usage("peers does not accept positional arguments")
	}
	if *format != "text" && *format != "json" {
		return usage("unsupported format %q", *format)
	}
	env, err := setup.Resolve()
	if err != nil {
		return cliWrap("internal", 1, "resolve paths", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := adminipc.Call(ctx, env.ControlEndpoint, adminipc.Request{
		Version: adminipc.Version, RequestID: uuid.NewString(), Operation: "peers.list",
	})
	if err != nil {
		var unavailable *adminipc.UnavailableError
		if !errors.As(err, &unavailable) {
			return &cliError{code: "control_unavailable", exit: 5, msg: fmt.Sprintf("management IPC protocol failed: %v", err)}
		}
		if !unavailable.Fallback {
			return &cliError{code: "control_unavailable", exit: 5, msg: fmt.Sprintf("management IPC is unavailable: %v", unavailable)}
		}
		return &cliError{code: "mind_not_running", exit: 4, msg: "Mind is not running"}
	}
	if !resp.OK {
		return cliIPCError(resp.Error)
	}
	var peers []management.PeerDTO
	if err := decodeResult(resp.Result, &peers); err != nil {
		return cliWrap("internal", 1, "decode peers", err)
	}
	if *format == "json" {
		return writeJSONOK(output, peers)
	}
	if len(peers) == 0 {
		fmt.Fprintln(output, "No connected peers.")
		return nil
	}
	fmt.Fprintln(output, "type  label           connected")
	for _, peer := range peers {
		fmt.Fprintf(output, "%-5s %-15s %s\n", peer.Type, peer.Label, peer.ConnectedAt.Format("01-02 15:04"))
	}
	return nil
}

func callManaged(operation string, params any, offline func(*management.Service, management.RequestMeta) (any, error)) (any, error) {
	env, err := setup.Resolve()
	if err != nil {
		return nil, cliWrap("internal", 1, "resolve paths", err)
	}
	req := adminipc.Request{Version: adminipc.Version, RequestID: uuid.NewString(), Operation: operation}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, cliWrap("internal", 1, "encode params", err)
		}
		req.Params = raw
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, ipcErr := adminipc.Call(ctx, env.ControlEndpoint, req)
	if ipcErr == nil {
		if !resp.OK {
			return nil, cliIPCError(resp.Error)
		}
		return resp.Result, nil
	}
	var unavailable *adminipc.UnavailableError
	if !errors.As(ipcErr, &unavailable) {
		if strings.HasSuffix(operation, ".add") || strings.HasSuffix(operation, ".remove") {
			return nil, &cliError{code: "result_unknown", exit: 5, msg: fmt.Sprintf("management IPC result is unknown: %v", ipcErr)}
		}
		return nil, &cliError{code: "control_unavailable", exit: 5, msg: fmt.Sprintf("management IPC protocol failed: %v", ipcErr)}
	}
	if !unavailable.Fallback {
		return nil, &cliError{code: "control_unavailable", exit: 5, msg: fmt.Sprintf("management IPC is unavailable: %v", unavailable)}
	}
	return callOffline(env, req.RequestID, offline)
}

func callOffline(env *setup.Env, requestID string, fn func(*management.Service, management.RequestMeta) (any, error)) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lock, err := statelock.Acquire(ctx, env.LockPath, statelock.Info{Mode: "offline_cli"})
	if err != nil {
		return nil, &cliError{code: "state_busy", exit: 5, msg: "Mind state is busy and management IPC is unavailable"}
	}
	defer lock.Close()
	initialized, err := setup.Init()
	if err != nil {
		return nil, cliWrap("internal", 1, "init environment", err)
	}
	db, err := store.New(initialized.DBPath)
	if err != nil {
		return nil, cliWrap("internal", 1, "initialize store", err)
	}
	defer db.Close()
	service := management.New(db, nil, management.Runtime{Mode: "offline_cli", HubEnabled: false})
	result, err := fn(service, management.RequestMeta{RequestID: requestID, Source: management.SourceOfflineCLI})
	if err != nil {
		return nil, cliFromManagement(err)
	}
	return result, nil
}

func faceScopesForCLI(profile, scopesText string) ([]protocol.FaceScope, error) {
	if profile != "" {
		return management.ExpandProfile(profile)
	}
	return management.ParseScopes(scopesText)
}

func validateCredentialAddFormat(format string) error {
	switch format {
	case "text", "json", "toml":
		return nil
	default:
		return usage("unsupported format %q", format)
	}
}

func isInteractiveTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}

func confirmRemoval(input io.Reader, prompt io.Writer, kind string, id int64, label string) error {
	selector := fmt.Sprintf("id %d", id)
	if label != "" {
		selector = fmt.Sprintf("label %q", label)
	}
	fmt.Fprintf(prompt, "Remove %s credential (%s)? [y/N] ", kind, selector)
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && len(answer) == 0 {
		return usage("could not read removal confirmation: %v", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	default:
		return usage("removal cancelled")
	}
}

func removeSelector(id int64, label string) (string, string, error) {
	if id > 0 && label != "" {
		return "", "", &management.Error{Code: "invalid_argument", Message: "--id and --label are mutually exclusive"}
	}
	if id > 0 {
		return "id", strconv.FormatInt(id, 10), nil
	}
	if label != "" {
		return "label", label, nil
	}
	return "", "", &management.Error{Code: "invalid_argument", Message: "missing --id or --label"}
}

func writeCredentialAdd(w io.Writer, kind string, credential management.SecretCredentialDTO, format string) error {
	switch format {
	case "json":
		return writeJSONOK(w, credential)
	case "toml":
		if kind == "hand" {
			fmt.Fprintln(w, "[server]")
			fmt.Fprintf(w, "token = %q\n", credential.Token)
			fmt.Fprintf(w, "application_key = %q\n\n", credential.ApplicationKey)
			fmt.Fprintln(w, "[hand]")
			fmt.Fprintf(w, "id = %q\n", credential.Label)
		} else {
			fmt.Fprintln(w, "[server]")
			fmt.Fprintf(w, "token = %q\n", credential.Token)
			fmt.Fprintf(w, "application_key = %q\n\n", credential.ApplicationKey)
			fmt.Fprintln(w, "[face]")
			fmt.Fprintf(w, "id = %q\n", credential.Label)
			fmt.Fprintln(w, "mode = \"tui\"")
		}
		return nil
	case "text":
		fmt.Fprintf(w, "%s created:\n", strings.Title(kind))
		fmt.Fprintf(w, "  id:              %d\n", credential.ID)
		fmt.Fprintf(w, "  label:           %s\n", credential.Label)
		fmt.Fprintf(w, "  token:           %s\n", credential.Token)
		fmt.Fprintf(w, "  application_key: %s\n", credential.ApplicationKey)
		if len(credential.Scopes) > 0 {
			fmt.Fprintf(w, "  scopes:          %s\n", strings.Join(credential.Scopes, ","))
		}
		return nil
	default:
		return usage("unsupported format %q", format)
	}
}

func writeCredentialTable(w io.Writer, kind string, credentials []management.CredentialDTO) {
	if len(credentials) == 0 {
		fmt.Fprintf(w, "No %s credentials.\n", strings.Title(kind))
		return
	}
	if kind == "face" {
		fmt.Fprintln(w, "id  face           scopes  created")
		for _, credential := range credentials {
			fmt.Fprintf(w, "%2d  %-14s %s  %s\n", credential.ID, credential.Label, strings.Join(credential.Scopes, ","), credential.CreatedAt.Format("01-02 15:04"))
		}
		return
	}
	fmt.Fprintln(w, "id  hand           created")
	for _, credential := range credentials {
		fmt.Fprintf(w, "%2d  %-14s %s\n", credential.ID, credential.Label, credential.CreatedAt.Format("01-02 15:04"))
	}
}

func writeStatus(w io.Writer, status management.StatusResult, format string) error {
	if format == "json" {
		return writeJSONOK(w, status)
	}
	if format != "text" {
		return usage("unsupported format %q", format)
	}
	fmt.Fprintf(w, "state:       %s\n", status.State)
	if status.State == "running" {
		fmt.Fprintf(w, "pid:         %d\n", status.PID)
		fmt.Fprintf(w, "mode:        %s\n", status.Mode)
		fmt.Fprintf(w, "hub_enabled: %v\n", status.HubEnabled)
		fmt.Fprintf(w, "ws_url:      %s\n", status.WSURL)
		fmt.Fprintf(w, "peers:       %d\n", status.PeerCount)
	}
	return nil
}

func writeConfig(w io.Writer, cfg *config.Config, format string) error {
	switch format {
	case "json":
		return writeJSONOK(w, cfg)
	case "toml":
		return toml.NewEncoder(w).Encode(cfg)
	default:
		return usage("unsupported format %q", format)
	}
}

func writeJSONOK(w io.Writer, result any) error {
	return json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func decodeResult(result any, target any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func scopeStrings(scopes []protocol.FaceScope) []string {
	values := make([]string, len(scopes))
	for i, scope := range scopes {
		values[i] = string(scope)
	}
	return values
}

func cliIPCError(resp *adminipc.ErrorResponse) error {
	if resp == nil {
		return &cliError{code: "internal", exit: 1, msg: "invalid IPC error response"}
	}
	return &cliError{code: resp.Code, exit: exitForCode(resp.Code), msg: resp.Message}
}

func cliFromManagement(err error) error {
	var managed *management.Error
	if errors.As(err, &managed) {
		return &cliError{code: managed.Code, exit: exitForCode(managed.Code), msg: managed.Error()}
	}
	return cliWrap("internal", 1, "management command", err)
}

func cliWrap(code string, exit int, prefix string, err error) error {
	return &cliError{code: code, exit: exit, msg: fmt.Sprintf("%s: %v", prefix, err)}
}

func usage(format string, args ...any) error {
	return &cliError{code: "usage", exit: 2, msg: fmt.Sprintf(format, args...)}
}

func exitForCode(code string) int {
	switch code {
	case "usage":
		return 2
	case "invalid_argument", "invalid_request", "invalid_config":
		return 3
	case "mind_not_running", "hub_disabled":
		return 4
	case "state_busy", "control_unavailable", "result_unknown":
		return 5
	case "not_found", "conflict", "permission_denied", "unauthorized":
		return 6
	default:
		return 1
	}
}
