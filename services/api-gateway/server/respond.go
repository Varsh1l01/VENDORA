package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/requestid"
)

// MaxBodyBytes bounds request bodies.
const MaxBodyBytes = 1 << 20

var marshal = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}

// writeProto writes a protobuf message as JSON. Zero values are emitted so
// clients always see every field (e.g. "total_cents": 0).
func writeProto(w http.ResponseWriter, code int, m proto.Message) {
	b, err := marshal.Marshal(m)
	if err != nil {
		writeProblem(w, nil, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

// Problem is an RFC 9457 problem details body.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, code int, detail string) {
	p := Problem{Type: "about:blank", Title: http.StatusText(code), Status: code, Detail: detail}
	if r != nil {
		p.RequestID = requestid.From(r.Context())
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(p)
}

var codeToHTTP = map[codes.Code]int{
	codes.OK:                 http.StatusOK,
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.OutOfRange:         http.StatusBadRequest,
	codes.Unauthenticated:    http.StatusUnauthorized,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.NotFound:           http.StatusNotFound,
	codes.AlreadyExists:      http.StatusConflict,
	codes.Aborted:            http.StatusConflict,
	codes.FailedPrecondition: http.StatusUnprocessableEntity,
	codes.ResourceExhausted:  http.StatusTooManyRequests,
	codes.Unavailable:        http.StatusServiceUnavailable,
	codes.DeadlineExceeded:   http.StatusGatewayTimeout,
	codes.Canceled:           499,
	codes.Unimplemented:      http.StatusNotImplemented,
}

// HTTPStatus maps a gRPC code to an HTTP status.
func HTTPStatus(c codes.Code) int {
	if s, ok := codeToHTTP[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// writeError maps a downstream gRPC error to a problem response. Messages of
// server-side failures are replaced by a generic text.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	st := status.Convert(err)
	code := HTTPStatus(st.Code())
	msg := st.Message()
	if code >= 500 {
		logx.From(r.Context()).Error("upstream call failed", "grpc_code", st.Code().String(), "error", msg)
		if code == http.StatusInternalServerError {
			msg = "internal error"
		}
	}
	writeProblem(w, r, code, msg)
}

// decode reads a JSON body strictly into v.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		writeProblem(w, r, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe):
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "request body too large")
		case errors.Is(err, io.EOF):
			writeProblem(w, r, http.StatusBadRequest, "request body is required")
		default:
			writeProblem(w, r, http.StatusBadRequest, "invalid JSON body: "+sanitizeJSONErr(err))
		}
		return false
	}
	if dec.More() {
		writeProblem(w, r, http.StatusBadRequest, "request body must contain a single JSON object")
		return false
	}
	return true
}

func sanitizeJSONErr(err error) string {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se):
		return "malformed JSON"
	case errors.As(err, &te):
		return "field " + te.Field + " has the wrong type"
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return strings.TrimPrefix(err.Error(), "json: ")
	}
	return "malformed JSON"
}
