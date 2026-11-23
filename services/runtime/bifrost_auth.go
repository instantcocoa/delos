package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"

	"github.com/instantcocoa/delos/pkg/semconv"
)

// bifrostKeyAuthenticator resolves a presented secret against Bifrost's
// governance store on every request — no in-process cache — so a revoked or
// expired key is refused on the next request on every replica, bounded only by
// one Postgres round-trip (milliseconds, far under the 1s target).
type bifrostKeyAuthenticator struct {
	cs configstore.ConfigStore
}

// WithBifrostKeyStore switches the gateway's key authentication to the Bifrost
// governance store. Every /v1 request re-reads the key's is_active/expires_at
// from Postgres via GetVirtualKeyByValue.
func WithBifrostKeyStore(store *BifrostKeyStore) HTTPServerOption {
	return func(s *HTTPServer) {
		s.bifrostKeys = &bifrostKeyAuthenticator{cs: store.ConfigStore()}
	}
}

// Authenticate resolves secret to a live KeyRecord. A missing, revoked
// (is_active=false) or expired key reads as ErrKeyNotFound (401); a store
// outage reads as ErrKeyStoreUnavailable (503).
func (a *bifrostKeyAuthenticator) Authenticate(ctx context.Context, secret string) (*KeyRecord, error) {
	if secret == "" {
		return nil, ErrKeyNotFound
	}
	vk, err := a.cs.GetVirtualKeyByValue(ctx, secret)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("%w: %w", ErrKeyStoreUnavailable, err)
	}
	if !vk.IsActiveValue() {
		return nil, ErrKeyNotFound
	}
	rec := &KeyRecord{
		ID:        vk.ID,
		Name:      vk.Name,
		SessionID: vk.Metadata[MetadataSessionID],
		OrgID:     vk.Metadata[MetadataOrgID],
		Subject:   vk.Metadata[MetadataSubject],
		Labels:    parseLabels(vk.Metadata[MetadataLabels]),
		Models:    parseModels(vk.Metadata[MetadataModels]),
		Active:    true,
		CreatedAt: vk.CreatedAt,
	}
	if vk.ExpiresAt != nil {
		rec.ExpiresAt = *vk.ExpiresAt
	}
	if rec.ExpiredAt(time.Now().UTC()) {
		return nil, ErrKeyNotFound
	}
	return rec, nil
}

// ---- key attribution (gap 5 / integration item 2) ----

// bifrostAttribution carries the key metadata DL-K mints onto every virtual
// key, resolved at authentication time and stamped onto the request's root span
// by the delosAttributionPlugin.
type bifrostAttribution struct {
	SessionID string
	OrgID     string
	Subject   string
	Labels    string // JSON object
}

type bifrostAttributionCtxKey struct{}

func (a bifrostAttribution) empty() bool {
	return a.SessionID == "" && a.OrgID == "" && a.Subject == "" && a.Labels == ""
}

// withBifrostAttribution attaches the resolved key's metadata to the request
// context so the Bifrost handlers can carry it into the Bifrost request context.
func withBifrostAttribution(r *http.Request, rec *KeyRecord) *http.Request {
	if rec == nil {
		return r
	}
	att := bifrostAttribution{SessionID: rec.SessionID, OrgID: rec.OrgID, Subject: rec.Subject}
	if len(rec.Labels) > 0 {
		if raw, err := json.Marshal(rec.Labels); err == nil {
			att.Labels = string(raw)
		}
	}
	return r.WithContext(context.WithValue(r.Context(), bifrostAttributionCtxKey{}, att))
}

// bifrostAttributionFromContext returns the resolved key attribution, or a zero
// value when none is present.
func bifrostAttributionFromContext(ctx context.Context) bifrostAttribution {
	att, _ := ctx.Value(bifrostAttributionCtxKey{}).(bifrostAttribution)
	return att
}

// delosAttributionPlugin stamps the key's session/org/subject/labels onto the
// request's root span. It runs inside Bifrost's request pipeline (PreLLMHook),
// after the trace has been created, using the tracer handle the framework
// stores on the Bifrost context — the same pattern the governance plugin uses.
type delosAttributionPlugin struct{}

func (delosAttributionPlugin) GetName() string { return "delos-attribution" }
func (delosAttributionPlugin) Cleanup() error  { return nil }

func (delosAttributionPlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}

func (delosAttributionPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	att, _ := ctx.Value(bifrostAttributionCtxKey{}).(bifrostAttribution)
	if att.empty() {
		return req, nil, nil
	}
	tracer, _ := ctx.Value(schemas.BifrostContextKeyTracer).(schemas.Tracer)
	traceID, _ := ctx.Value(schemas.BifrostContextKeyTraceID).(string)
	if tracer == nil || traceID == "" {
		return req, nil, nil
	}
	root := tracer.SpanFromHandle(tracer.GetSpanHandleByID(traceID, nil))
	if root == nil {
		return req, nil, nil
	}
	if att.SessionID != "" {
		root.SetAttribute(semconv.GenAIConversationID, att.SessionID)
	}
	if att.OrgID != "" {
		root.SetAttribute(semconv.DelosOrgID, att.OrgID)
	}
	if att.Subject != "" {
		root.SetAttribute(semconv.DelosSubject, att.Subject)
	}
	if att.Labels != "" {
		root.SetAttribute(semconv.DelosLabels, att.Labels)
	}
	return req, nil, nil
}

func (delosAttributionPlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bErr, nil
}
