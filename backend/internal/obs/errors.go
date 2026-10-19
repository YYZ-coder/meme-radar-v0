package obs

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// Code is a stable business error / event code (18-observability §1).
// API codes are lower_snake and appear in ErrorResponse.error.code (api/openapi.yaml ErrorCode).
// Internal event codes are "area.reason" and appear only in logs and alerts.
type Code string

// API error codes. Keep in sync with api/openapi.yaml#/components/schemas/ErrorCode
// (enforced by TestAPICodesMatchOpenAPI).
const (
	CodeInvalidParam   Code = "invalid_param"
	CodeInvalidRange   Code = "invalid_range"
	CodeUnauthorized   Code = "unauthorized"
	CodeForbidden      Code = "forbidden"
	CodeNotFound       Code = "not_found"
	CodeImportClosed   Code = "import_closed"
	CodeQueryExpired   Code = "query_expired"
	CodeFileTooLarge   Code = "file_too_large"
	CodeTooManyRows    Code = "too_many_rows"
	CodeInvalidFile    Code = "invalid_file"
	CodeSecretDetected Code = "secret_detected"
	CodeRateLimited    Code = "rate_limited"
	CodeBusy           Code = "busy"
	CodeAPIUnavailable Code = "api_unavailable"
	CodeInternal       Code = "internal"
)

// Internal event codes.
const (
	// ingest — values after the dot match block_jobs.last_error_class / collection_gaps.reason.
	IngestRPCRetryable       Code = "ingest.rpc_retryable"
	IngestRPCConfig          Code = "ingest.rpc_config"
	IngestBlockNotFound      Code = "ingest.block_not_found"
	IngestHistoryUnavailable Code = "ingest.history_unavailable"
	IngestBlockIncomplete    Code = "ingest.block_incomplete"
	IngestReorgDetected      Code = "ingest.reorg_detected"
	IngestGapRecorded        Code = "ingest.gap_recorded"
	IngestLeaseExpired       Code = "ingest.lease_expired"
	IngestBudgetPaused       Code = "ingest.budget_paused"
	// parse
	ParseUnknownDiscriminator Code = "parse.unknown_discriminator"
	ParseDecodeFailed         Code = "parse.decode_failed"
	ParseConservationFailed   Code = "parse.conservation_failed"
	ParseInternal             Code = "parse.internal"
	ParseAttributionPending   Code = "parse.attribution_pending"
	ParseReparse              Code = "parse.reparse"
	// compute / pricing / holding
	ComputePriceStale       Code = "compute.price_stale"
	ComputePriceMissing     Code = "compute.price_missing"
	ComputeMainPoolSwitched Code = "compute.main_pool_switched"
	ComputeRecompute        Code = "compute.recompute"
	HoldingPartialCoverage  Code = "holding.partial_coverage"
	HoldingNegativeBalance  Code = "holding.negative_balance"
	// smart money
	SmartMoneyImportApplied Code = "smartmoney.import_applied"
	SmartMoneyListVersion   Code = "smartmoney.list_version_changed"
	// api
	APISlowQuery       Code = "api.slow_query"
	APISnapshotExpired Code = "api.snapshot_expired"
	APIAdminDenied     Code = "api.admin_denied"
	// runtime
	RuntimePanicRecovered     Code = "runtime.panic_recovered"
	RuntimeGoroutineRestarted Code = "runtime.goroutine_restarted"
	RuntimeDBUnavailable      Code = "runtime.db_unavailable"
)

// Spec describes how a code is surfaced.
type Spec struct {
	HTTPStatus int        // 0 for internal event codes
	Level      slog.Level // level used when the code is logged
	Alert      bool       // should page / show in admin health
}

// Catalog is the single registry of codes; adding a code here is a contract change.
var Catalog = map[Code]Spec{
	CodeInvalidParam:   {http.StatusBadRequest, slog.LevelInfo, false},
	CodeInvalidRange:   {http.StatusBadRequest, slog.LevelInfo, false},
	CodeUnauthorized:   {http.StatusUnauthorized, slog.LevelInfo, false},
	CodeForbidden:      {http.StatusForbidden, slog.LevelWarn, false},
	CodeNotFound:       {http.StatusNotFound, slog.LevelInfo, false},
	CodeImportClosed:   {http.StatusConflict, slog.LevelInfo, false},
	CodeQueryExpired:   {http.StatusGone, slog.LevelInfo, false},
	CodeFileTooLarge:   {http.StatusRequestEntityTooLarge, slog.LevelInfo, false},
	CodeTooManyRows:    {http.StatusRequestEntityTooLarge, slog.LevelInfo, false},
	CodeInvalidFile:    {http.StatusBadRequest, slog.LevelInfo, false},
	CodeSecretDetected: {http.StatusBadRequest, slog.LevelWarn, false},
	CodeRateLimited:    {http.StatusTooManyRequests, slog.LevelWarn, false},
	CodeBusy:           {http.StatusServiceUnavailable, slog.LevelWarn, true},
	CodeAPIUnavailable: {http.StatusServiceUnavailable, slog.LevelError, true},
	CodeInternal:       {http.StatusInternalServerError, slog.LevelError, true},

	IngestRPCRetryable:       {0, slog.LevelWarn, false},
	IngestRPCConfig:          {0, slog.LevelError, true},
	IngestBlockNotFound:      {0, slog.LevelWarn, false},
	IngestHistoryUnavailable: {0, slog.LevelError, true},
	IngestBlockIncomplete:    {0, slog.LevelWarn, false},
	IngestReorgDetected:      {0, slog.LevelWarn, false},
	IngestGapRecorded:        {0, slog.LevelError, true},
	IngestLeaseExpired:       {0, slog.LevelWarn, false},
	IngestBudgetPaused:       {0, slog.LevelError, true},

	ParseUnknownDiscriminator: {0, slog.LevelWarn, false},
	ParseDecodeFailed:         {0, slog.LevelError, true},
	ParseConservationFailed:   {0, slog.LevelError, true},
	ParseInternal:             {0, slog.LevelError, true},
	ParseAttributionPending:   {0, slog.LevelInfo, false},
	ParseReparse:              {0, slog.LevelInfo, false},

	ComputePriceStale:       {0, slog.LevelWarn, false},
	ComputePriceMissing:     {0, slog.LevelError, true},
	ComputeMainPoolSwitched: {0, slog.LevelInfo, false},
	ComputeRecompute:        {0, slog.LevelInfo, false},
	HoldingPartialCoverage:  {0, slog.LevelInfo, false},
	HoldingNegativeBalance:  {0, slog.LevelError, true},

	SmartMoneyImportApplied: {0, slog.LevelInfo, false},
	SmartMoneyListVersion:   {0, slog.LevelInfo, false},

	APISlowQuery:       {0, slog.LevelWarn, false},
	APISnapshotExpired: {0, slog.LevelInfo, false},
	APIAdminDenied:     {0, slog.LevelWarn, false},

	RuntimePanicRecovered:     {0, slog.LevelError, true},
	RuntimeGoroutineRestarted: {0, slog.LevelWarn, true},
	RuntimeDBUnavailable:      {0, slog.LevelError, true},
}

// Error is a business error carrying a catalog code.
type Error struct {
	Code    Code
	Message string // safe to show to API clients
	Field   string // offending parameter for 400s
	Err     error  // internal cause; logged, never returned to clients
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// New returns a business error.
func New(code Code, message string) *Error { return &Error{Code: code, Message: message} }

// Field returns a 400-style error pointing at a request parameter.
func Field(code Code, field, message string) *Error {
	return &Error{Code: code, Message: message, Field: field}
}

// Wrap attaches a code to an internal cause.
func Wrap(code Code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}

// As extracts an *Error; anything else becomes CodeInternal.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Message: "internal error", Err: err}
}

// Status is the HTTP status for a code (500 for internal event codes).
func (c Code) Status() int {
	if s := Catalog[c].HTTPStatus; s != 0 {
		return s
	}
	return http.StatusInternalServerError
}

// IsAPI reports whether the code may appear in API responses.
func (c Code) IsAPI() bool { return Catalog[c].HTTPStatus != 0 }
