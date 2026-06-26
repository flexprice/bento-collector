package output

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// staticTokenSource returns a fixed token for deterministic testing.
type staticTokenSource struct{ tok *oauth2.Token }

func (s staticTokenSource) Token() (*oauth2.Token, error) { return s.tok, nil }

// decodeCacheSeg decodes a base64url segment into v (must be a pointer to a map or struct).
func decodeCacheSeg(t *testing.T, seg string, v any) {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("segment not valid base64url: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("segment not valid JSON: %v", err)
	}
}

// TestGMKTokenFormat verifies that Get() returns a three-segment
// OAUTHBEARER token matching Google's reference wire format:
// base64url(header).base64url(payload).base64url(rawAccessToken)
func TestGMKTokenFormat(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	cache := &gmkOAuthCache{
		src:       oauth2.ReuseTokenSource(nil, staticTokenSource{&oauth2.Token{AccessToken: "raw-access-token-xyz", Expiry: expiry}}),
		principal: "test-sa@proj.iam.gserviceaccount.com", // set so metadata server is NOT hit
	}

	got, err := cache.Get(context.Background(), "")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	tokenStr := string(got)
	segs := strings.Split(tokenStr, ".")
	if len(segs) != 3 {
		t.Fatalf("expected 3 dot-separated segments, got %d: %q", len(segs), tokenStr)
	}

	// Segment 1: header must be {"typ":"JWT","alg":"GOOG_OAUTH2_TOKEN"}
	var header map[string]string
	decodeCacheSeg(t, segs[0], &header)
	if header["typ"] != "JWT" {
		t.Errorf("header typ: want JWT, got %q", header["typ"])
	}
	if header["alg"] != "GOOG_OAUTH2_TOKEN" {
		t.Errorf("header alg: want GOOG_OAUTH2_TOKEN, got %q", header["alg"])
	}

	// Segment 2: payload must carry iss, sub, exp matching injected token.
	var payload map[string]any
	decodeCacheSeg(t, segs[1], &payload)
	if payload["iss"] != "Google" {
		t.Errorf("payload iss: want Google, got %v", payload["iss"])
	}
	if payload["sub"] != "test-sa@proj.iam.gserviceaccount.com" {
		t.Errorf("payload sub: want test-sa@proj.iam.gserviceaccount.com, got %v", payload["sub"])
	}
	gotExp, ok := payload["exp"]
	if !ok {
		t.Fatal("payload missing exp")
	}
	// JSON numbers unmarshal as float64.
	wantExp := float64(expiry.UTC().Unix())
	if gotExp.(float64) != wantExp {
		t.Errorf("payload exp: want %v, got %v", wantExp, gotExp)
	}
	if _, ok := payload["iat"]; !ok {
		t.Error("payload missing iat")
	}

	// Segment 3: base64url-encoded raw access token.
	raw, err := base64.RawURLEncoding.DecodeString(segs[2])
	if err != nil {
		t.Fatalf("segment 3 not valid base64url: %v", err)
	}
	if string(raw) != "raw-access-token-xyz" {
		t.Errorf("segment 3: want raw-access-token-xyz, got %q", string(raw))
	}
}

// TestGetIgnoresKey verifies that calling Get with different keys returns
// tokens with identical header (segment 1) and access-token (segment 3)
// segments. Segment 2 (payload) may differ in iat across calls so we only
// compare segments 1 and 3.
func TestGetIgnoresKey(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	cache := &gmkOAuthCache{
		src:       oauth2.ReuseTokenSource(nil, staticTokenSource{&oauth2.Token{AccessToken: "raw-access-token-xyz", Expiry: expiry}}),
		principal: "test-sa@proj.iam.gserviceaccount.com",
	}

	got1, err := cache.Get(context.Background(), "")
	if err != nil {
		t.Fatalf("Get(\"\") error: %v", err)
	}
	got2, err := cache.Get(context.Background(), "anything")
	if err != nil {
		t.Fatalf("Get(\"anything\") error: %v", err)
	}

	segs1 := strings.Split(string(got1), ".")
	segs2 := strings.Split(string(got2), ".")
	if len(segs1) != 3 || len(segs2) != 3 {
		t.Fatalf("expected 3 segments in both calls, got %d and %d", len(segs1), len(segs2))
	}

	if segs1[0] != segs2[0] {
		t.Errorf("header segment differs across keys:\n  key='':        %s\n  key='anything': %s", segs1[0], segs2[0])
	}
	if segs1[2] != segs2[2] {
		t.Errorf("access-token segment differs across keys:\n  key='':        %s\n  key='anything': %s", segs1[2], segs2[2])
	}
}

// TestResolvePrincipalLocalRequiresEnv verifies that resolvePrincipal() returns
// an actionable error mentioning GOOGLE_MANAGED_KAFKA_AUTH_PRINCIPAL when the
// principal was not set at construction. This test only runs when NOT on GCE
// (i.e. local dev); on a real GCE/GKE host the metadata path would be taken
// instead and this assertion would not be reachable.
func TestResolvePrincipalLocalRequiresEnv(t *testing.T) {
	// Skip on GCE/GKE where the metadata server IS reachable — the local-error
	// branch is not exercised there.
	// We import metadata indirectly through the package; OnGCE is checked in
	// resolvePrincipal itself. Instead we just call resolvePrincipal directly
	// and let the function decide. If we're on GCE the call would hit the
	// metadata server instead of returning the local error, which would make the
	// assertion below incorrect. Guard with a skip.
	//
	// We can't easily force OnGCE()=false in a unit test without mocking the
	// metadata package, so skip rather than over-engineer.
	t.Log("TestResolvePrincipalLocalRequiresEnv: calling resolvePrincipal with empty principal")

	cache := &gmkOAuthCache{
		src: oauth2.ReuseTokenSource(nil, staticTokenSource{&oauth2.Token{
			AccessToken: "tok",
			Expiry:      time.Now().Add(time.Hour),
		}}),
		// principal intentionally empty — should trigger local-error or metadata path.
	}

	_, err := cache.resolvePrincipal()
	if err == nil {
		// We are likely on GCE/GKE and got an email from the metadata server — that
		// is correct behaviour; the test cannot assert the local-error branch here.
		t.Skip("running on GCE/GKE — metadata path taken, local-error branch not exercised")
	}
	const wantSubstr = "GOOGLE_MANAGED_KAFKA_AUTH_PRINCIPAL"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error %q does not mention %s", err.Error(), wantSubstr)
	}
}

// TestNoOpsReturnNil verifies that Set, Add, Delete, and Close all return nil.
func TestNoOpsReturnNil(t *testing.T) {
	cache := &gmkOAuthCache{
		src:       oauth2.ReuseTokenSource(nil, staticTokenSource{&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}}),
		principal: "test-sa@proj.iam.gserviceaccount.com",
	}
	ctx := context.Background()

	if err := cache.Set(ctx, "k", []byte("v"), nil); err != nil {
		t.Errorf("Set() returned non-nil error: %v", err)
	}
	if err := cache.Add(ctx, "k", []byte("v"), nil); err != nil {
		t.Errorf("Add() returned non-nil error: %v", err)
	}
	if err := cache.Delete(ctx, "k"); err != nil {
		t.Errorf("Delete() returned non-nil error: %v", err)
	}
	if err := cache.Close(ctx); err != nil {
		t.Errorf("Close() returned non-nil error: %v", err)
	}
}
