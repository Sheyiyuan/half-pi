package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// ToolDisplayProjection 是一次 Chat 工具调用的持久化用户展示投影。
type ToolDisplayProjection struct {
	ConversationID    string
	RequestID         string
	Ordinal           int64
	Tool              string
	DetailMode        protocol.FaceDetailMode
	ArgsDigest        string
	ArgsBytes         int
	ArgsTruncated     bool
	Args              *protocol.ToolArgsView
	Result            *protocol.ToolOutputView
	OutputBytes       int
	OutputDigest      string
	Truncated         bool
	ErrorCategory     string
	Success           bool
	Complete          bool
	ProjectionVersion string
	ScanWarnings      []string
	CreatedAt         time.Time
	CompletedAt       time.Time
}

// CreateToolDisplayProjection 保存 admission 时冻结的参数展示投影。
func (s *Store) CreateToolDisplayProjection(record ToolDisplayProjection) error {
	if record.ConversationID == "" || record.RequestID == "" || record.Ordinal <= 0 || record.Tool == "" ||
		record.ArgsDigest == "" || record.CreatedAt.IsZero() || !validToolDetailMode(record.DetailMode) {
		return fmt.Errorf("tool display projection fields are required")
	}
	if record.DetailMode == protocol.FaceDetailModeSummary && (record.Args != nil || record.ProjectionVersion != "") {
		return fmt.Errorf("summary tool display projection must not contain arguments")
	}
	if record.DetailMode == protocol.FaceDetailModeTransparent &&
		(record.Args == nil || record.ProjectionVersion != protocol.FaceToolDisplayProjectionVersion ||
			record.Args.ProjectionVersion != record.ProjectionVersion || record.ArgsBytes != record.Args.Bytes ||
			record.ArgsTruncated != record.Args.Truncated || !slices.Equal(record.ScanWarnings, record.Args.Warnings)) {
		return fmt.Errorf("transparent tool arguments projection is invalid")
	}
	args, err := encodeOptionalProjection(record.Args)
	if err != nil {
		return fmt.Errorf("encode tool args projection: %w", err)
	}
	warnings, err := json.Marshal(record.ScanWarnings)
	if err != nil {
		return fmt.Errorf("encode tool display warnings: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO tool_display_projections
		(conversation_id, request_id, ordinal, tool, detail_mode, args_digest, args_bytes, args_truncated, args_projection,
		 projection_version, scan_warnings, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.ConversationID, record.RequestID, record.Ordinal,
		record.Tool, record.DetailMode, record.ArgsDigest, record.ArgsBytes, record.ArgsTruncated, args,
		record.ProjectionVersion, string(warnings), record.CreatedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("create tool display projection: %w", err)
	}
	return nil
}

// CompleteToolDisplayProjection 保存可靠终态输出；只允许未完成记录完成一次。
func (s *Store) CompleteToolDisplayProjection(conversationID, requestID string, ordinal int64, result *protocol.ToolOutputView,
	success bool, projectionVersion string, warnings []string, outputBytes int, outputDigest string, truncated bool,
	errorCategory string, completedAt time.Time) error {
	if conversationID == "" || requestID == "" || ordinal <= 0 || completedAt.IsZero() {
		return fmt.Errorf("tool display completion fields are required")
	}
	if outputBytes < 0 || outputBytes > 0 && outputDigest == "" {
		return fmt.Errorf("tool display output metadata is invalid")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tool display completion: %w", err)
	}
	defer tx.Rollback()
	var detailMode protocol.FaceDetailMode
	var storedVersion, storedWarnings string
	var complete int
	err = tx.QueryRow(`SELECT detail_mode, projection_version, scan_warnings, complete
		FROM tool_display_projections WHERE conversation_id = ? AND request_id = ? AND ordinal = ?`,
		conversationID, requestID, ordinal).Scan(&detailMode, &storedVersion, &storedWarnings, &complete)
	if err == sql.ErrNoRows {
		return fmt.Errorf("tool display projection is missing or complete")
	}
	if err != nil {
		return fmt.Errorf("read tool display projection for completion: %w", err)
	}
	if complete != 0 {
		return fmt.Errorf("tool display projection is missing or complete")
	}
	if detailMode == protocol.FaceDetailModeSummary && (result != nil || projectionVersion != "") {
		return fmt.Errorf("summary tool display completion must not contain a result")
	}
	if detailMode == protocol.FaceDetailModeTransparent &&
		(result == nil || storedVersion != protocol.FaceToolDisplayProjectionVersion || projectionVersion != storedVersion ||
			result.OutputBytes != outputBytes || result.Digest != outputDigest || result.Truncated != truncated ||
			!slices.Equal(result.Warnings, warnings)) {
		return fmt.Errorf("transparent tool result projection is invalid")
	}
	encoded, err := encodeOptionalProjection(result)
	if err != nil {
		return fmt.Errorf("encode tool result projection: %w", err)
	}
	var admissionWarnings []string
	if err := json.Unmarshal([]byte(storedWarnings), &admissionWarnings); err != nil {
		return fmt.Errorf("decode admission tool display warnings: %w", err)
	}
	warnings = mergeToolDisplayWarnings(admissionWarnings, warnings)
	warningJSON, err := json.Marshal(warnings)
	if err != nil {
		return fmt.Errorf("encode tool result warnings: %w", err)
	}
	changed, err := tx.Exec(`UPDATE tool_display_projections SET result_projection = ?, success = ?, complete = 1,
		output_bytes = ?, output_digest = ?, truncated = ?, error_category = ?,
		projection_version = CASE WHEN ? = '' THEN projection_version ELSE ? END,
		scan_warnings = ?, completed_at = ?
		WHERE conversation_id = ? AND request_id = ? AND ordinal = ? AND complete = 0`,
		encoded, success, outputBytes, outputDigest, truncated, errorCategory,
		projectionVersion, projectionVersion, string(warningJSON), completedAt.UnixMilli(),
		conversationID, requestID, ordinal)
	if err != nil {
		return fmt.Errorf("complete tool display projection: %w", err)
	}
	count, err := changed.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete tool display projection rows: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("tool display projection is missing or complete")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tool display completion: %w", err)
	}
	return nil
}

func mergeToolDisplayWarnings(groups ...[]string) []string {
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, warning := range group {
			if warning != "" {
				seen[warning] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for warning := range seen {
		result = append(result, warning)
	}
	sort.Strings(result)
	return result
}

// ListToolDisplayProjections 返回 conversation 的新式工具展示历史。
func (s *Store) ListToolDisplayProjections(conversationID string) ([]ToolDisplayProjection, error) {
	rows, err := s.db.Query(`SELECT conversation_id, request_id, ordinal, tool, detail_mode, args_digest,
		args_bytes, args_truncated, args_projection, result_projection, output_bytes, output_digest, truncated, error_category,
		success, complete, projection_version, scan_warnings, created_at, completed_at
		FROM tool_display_projections WHERE conversation_id = ? ORDER BY created_at, ordinal`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list tool display projections: %w", err)
	}
	defer rows.Close()
	var result []ToolDisplayProjection
	for rows.Next() {
		record, err := scanToolDisplayProjection(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tool display projections rows: %w", err)
	}
	return result, nil
}

type toolDisplayScanner interface{ Scan(...any) error }

func scanToolDisplayProjection(scanner toolDisplayScanner) (ToolDisplayProjection, error) {
	var record ToolDisplayProjection
	var argsJSON, resultJSON, warningsJSON string
	var success, complete int
	var createdAt, completedAt int64
	if err := scanner.Scan(&record.ConversationID, &record.RequestID, &record.Ordinal, &record.Tool, &record.DetailMode,
		&record.ArgsDigest, &record.ArgsBytes, &record.ArgsTruncated, &argsJSON, &resultJSON,
		&record.OutputBytes, &record.OutputDigest, &record.Truncated, &record.ErrorCategory,
		&success, &complete, &record.ProjectionVersion,
		&warningsJSON, &createdAt, &completedAt); err != nil {
		return ToolDisplayProjection{}, err
	}
	if !validToolDetailMode(record.DetailMode) || record.Ordinal <= 0 || record.ConversationID == "" || record.RequestID == "" {
		return ToolDisplayProjection{}, fmt.Errorf("invalid stored tool display projection")
	}
	if argsJSON != "" {
		record.Args = &protocol.ToolArgsView{}
		if err := json.Unmarshal([]byte(argsJSON), record.Args); err != nil {
			return ToolDisplayProjection{}, fmt.Errorf("decode tool args projection: %w", err)
		}
	}
	if resultJSON != "" {
		record.Result = &protocol.ToolOutputView{}
		if err := json.Unmarshal([]byte(resultJSON), record.Result); err != nil {
			return ToolDisplayProjection{}, fmt.Errorf("decode tool result projection: %w", err)
		}
	}
	if err := json.Unmarshal([]byte(warningsJSON), &record.ScanWarnings); err != nil {
		return ToolDisplayProjection{}, fmt.Errorf("decode tool display warnings: %w", err)
	}
	record.Success, record.Complete = success == 1, complete == 1
	record.CreatedAt = time.UnixMilli(createdAt).UTC()
	if completedAt > 0 {
		record.CompletedAt = time.UnixMilli(completedAt).UTC()
	}
	return record, nil
}

func encodeOptionalProjection(value any) (string, error) {
	if value == nil || reflect.ValueOf(value).Kind() == reflect.Pointer && reflect.ValueOf(value).IsNil() {
		return "", nil
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func validToolDetailMode(mode protocol.FaceDetailMode) bool {
	return mode == protocol.FaceDetailModeTransparent || mode == protocol.FaceDetailModeSummary
}
