package researchrefresh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshSuccessAndProviderHash(t *testing.T) {
	body := []byte("\ufeff# Official\r\n\r\nContent.  \r\n")
	rawHash := sha256.Sum256(body)
	lastModified := "Tue, 11 Aug 2026 19:00:00 GMT"
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Error("refresh forwarded credentials")
		}
		if got := request.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("Accept-Encoding = %q, want identity", got)
		}
		response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		response.Header().Set("ETag", `"fixture-v1"`)
		response.Header().Set("Last-Modified", lastModified)
		response.Header().Set("X-Content-SHA256", hex.EncodeToString(rawHash[:]))
		_, _ = response.Write(body)
	}))
	defer server.Close()

	feed := refreshTestFeed("official", KindMarkdown)
	feed.VerifyProviderSHA256 = true
	now := time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
	result, err := Refresh(context.Background(), feed, nil, Options{Client: remappedTestClient(server), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusFetched || !result.Due || !result.Changed {
		t.Fatalf("unexpected result: %#v", result)
	}
	if got, want := string(result.SemanticContent), "# Official\n\nContent.\n"; got != want {
		t.Fatalf("semantic content = %q, want %q", got, want)
	}
	if result.RawSHA256 != hex.EncodeToString(rawHash[:]) {
		t.Fatalf("raw hash = %s", result.RawSHA256)
	}
	receipt, ok := result.CandidateReceipt()
	if !ok {
		t.Fatal("fetched result has no candidate receipt")
	}
	if receipt.ProviderSHA256 != result.RawSHA256 || receipt.ETag != `"fixture-v1"` || receipt.LastModified != lastModified {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
	encoded, err := EncodeReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Official") || strings.Contains(string(encoded), "Content.") {
		t.Fatalf("receipt leaked fetched content: %s", encoded)
	}
	result.SemanticContent[0] = 'X'
	second, _ := result.CandidateReceipt()
	if second.SemanticSHA256 != receipt.SemanticSHA256 {
		t.Fatal("mutating ephemeral content changed candidate receipt")
	}
}

func TestRefreshSemanticNormalizationIsStable(t *testing.T) {
	var body atomic.Value
	body.Store("line one \t\r\nline two\t \r\n \t\r\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(response, body.Load().(string))
	}))
	defer server.Close()

	feed := refreshTestFeed("semantic", KindText)
	client := remappedTestClient(server)
	first, err := Refresh(context.Background(), feed, nil, Options{Client: client, Now: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := first.CandidateReceipt()
	body.Store("\nline one\nline two\n")
	second, err := Refresh(context.Background(), feed, &previous, Options{
		Client: client,
		Now:    fixedTime().Add(time.Hour),
		Force:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.RawSHA256 == first.RawSHA256 {
		t.Fatal("raw hashes unexpectedly match")
	}
	if second.SemanticSHA256 != first.SemanticSHA256 || second.Changed {
		t.Fatalf("semantic normalization was unstable: first=%s second=%s changed=%t", first.SemanticSHA256, second.SemanticSHA256, second.Changed)
	}
}

func TestNormalizeTextStripsOnlyTrailingHorizontalWhitespace(t *testing.T) {
	normalized, err := normalizeText([]byte(" \t\r\nkeep  internal\tspace \t\r\nsecond\t\r\n\t \r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(normalized), "keep  internal\tspace\nsecond\n"; got != want {
		t.Fatalf("normalized text = %q, want %q", got, want)
	}
}

func TestRefreshSupportsHTML(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=UTF-8")
		fmt.Fprint(response, "<html>\r\n<body>official</body>\r\n</html>\r\n")
	}))
	defer server.Close()

	result, err := Refresh(context.Background(), refreshTestFeed("html", KindHTML), nil, Options{Client: remappedTestClient(server), Now: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.SemanticContent); got != "<html>\n<body>official</body>\n</html>\n" {
		t.Fatalf("HTML canonical content = %q", got)
	}
}

func TestRefreshConditionalNotModified(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if calls.Add(1) == 1 {
			response.Header().Set("Content-Type", "text/plain")
			response.Header().Set("ETag", `"v1"`)
			response.Header().Set("Last-Modified", "Tue, 11 Aug 2026 19:00:00 GMT")
			fmt.Fprint(response, "unchanged\n")
			return
		}
		if request.Header.Get("If-None-Match") != `"v1"` {
			t.Errorf("If-None-Match = %q", request.Header.Get("If-None-Match"))
		}
		if request.Header.Get("If-Modified-Since") != "Tue, 11 Aug 2026 19:00:00 GMT" {
			t.Errorf("If-Modified-Since = %q", request.Header.Get("If-Modified-Since"))
		}
		response.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	feed := refreshTestFeed("conditional", KindText)
	client := remappedTestClient(server)
	first, err := Refresh(context.Background(), feed, nil, Options{Client: client, Now: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := first.CandidateReceipt()
	secondNow := fixedTime().Add(24 * time.Hour)
	second, err := Refresh(context.Background(), feed, &previous, Options{Client: client, Now: secondNow})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != StatusNotModified || second.Changed || len(second.SemanticContent) != 0 {
		t.Fatalf("unexpected 304 result: %#v", second)
	}
	receipt, ok := second.CandidateReceipt()
	if !ok || !receipt.FetchedAt.Equal(previous.FetchedAt) || !receipt.CheckedAt.Equal(secondNow) {
		t.Fatalf("304 receipt did not preserve fetch evidence: %#v", receipt)
	}
}

func TestRefreshRejectsUnconditionalNotModified(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	feed := refreshTestFeed("unconditional", KindText)
	previous := validReceipt(feed.ID, fixedTime().Add(-24*time.Hour))
	_, err := Refresh(context.Background(), feed, &previous, Options{
		Client: remappedTestClient(server),
		Now:    fixedTime(),
	})
	if err == nil || !strings.Contains(err.Error(), "304 without a conditional request") {
		t.Fatalf("unconditional 304 error = %v", err)
	}
}

func TestRefreshAllowsCuratedRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Host == "developers.openai.com" {
			http.Redirect(response, request, "https://learn.chatgpt.com/docs/codex-manual.md", http.StatusFound)
			return
		}
		response.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(response, "manual\n")
	}))
	defer server.Close()

	feed := testFeed("curated-redirect", "https://developers.openai.com/codex/codex-manual.md", KindMarkdown)
	feed.AllowedRedirectDomains = []string{"learn.chatgpt.com"}
	result, err := Refresh(context.Background(), feed, nil, Options{Client: remappedTestClient(server), Now: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := result.CandidateReceipt()
	if !ok || receipt.FinalURL != "https://learn.chatgpt.com/docs/codex-manual.md" {
		t.Fatalf("unexpected redirected receipt: %#v", receipt)
	}
}

func TestRefreshDeniesCrossDomainRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "https://example.invalid/official", http.StatusFound)
	}))
	defer server.Close()

	_, err := Refresh(context.Background(), refreshTestFeed("redirect", KindText), nil, Options{Client: remappedTestClient(server), Now: fixedTime()})
	if !errors.Is(err, ErrRedirectDenied) {
		t.Fatalf("redirect error = %v, want ErrRedirectDenied", err)
	}
}

func TestRefreshDeniesOffPolicyRedirectPath(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "https://raw.githubusercontent.com/modelcontextprotocol/modelcontextprotocol/main/private.json", http.StatusFound)
	}))
	defer server.Close()

	_, err := Refresh(context.Background(), refreshTestFeed("redirect-path", KindText), nil, Options{Client: remappedTestClient(server), Now: fixedTime()})
	if !errors.Is(err, ErrRedirectDenied) {
		t.Fatalf("redirect error = %v, want ErrRedirectDenied", err)
	}
}

func TestRefreshRejectsOversizedBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(response, "123456789")
	}))
	defer server.Close()

	_, err := Refresh(context.Background(), refreshTestFeed("large", KindText), nil, Options{
		Client:           remappedTestClient(server),
		Now:              fixedTime(),
		MaxResponseBytes: 8,
	})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("oversize error = %v, want ErrResponseTooLarge", err)
	}
}

func TestRefreshEnforcesRequestTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	_, err := Refresh(context.Background(), refreshTestFeed("timeout", KindText), nil, Options{
		Client:  remappedTestClient(server),
		Now:     fixedTime(),
		Timeout: 20 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v, want context deadline exceeded", err)
	}
}

func TestRefreshRejectsBadProviderHash(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain")
		response.Header().Set("X-Content-SHA256", strings.Repeat("0", 64))
		fmt.Fprint(response, "authentic content")
	}))
	defer server.Close()
	feed := refreshTestFeed("bad-hash", KindText)
	feed.VerifyProviderSHA256 = true

	_, err := Refresh(context.Background(), feed, nil, Options{Client: remappedTestClient(server), Now: fixedTime()})
	if !errors.Is(err, ErrProviderHash) {
		t.Fatalf("provider hash error = %v, want ErrProviderHash", err)
	}
}

func TestRefreshRejectsUnexpectedContentType(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/octet-stream")
		fmt.Fprint(response, "opaque")
	}))
	defer server.Close()

	_, err := Refresh(context.Background(), refreshTestFeed("type", KindText), nil, Options{Client: remappedTestClient(server), Now: fixedTime()})
	if !errors.Is(err, ErrUnexpectedContentType) {
		t.Fatalf("content type error = %v, want ErrUnexpectedContentType", err)
	}
}

func TestRefreshGitHubReleaseCanonicalExtraction(t *testing.T) {
	secretBody := "Release notes that must never be stored.\r\n"
	upstream := fmt.Sprintf(`{
  "url": "https://api.github.com/repos/openai/codex/releases/1",
  "tag_name": "v1.2.3",
  "published_at": "2026-08-11T18:30:00Z",
  "html_url": "https://github.com/openai/codex/releases/tag/v1.2.3",
  "prerelease": false,
  "draft": false,
  "target_commitish": "main",
  "body": %q,
  "assets": [{"name":"ignored"}]
}`, secretBody)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/vnd.github+json; charset=utf-8")
		fmt.Fprint(response, upstream)
	}))
	defer server.Close()

	result, err := Refresh(context.Background(), refreshTestFeed("release", KindGitHubLatestRelease), nil, Options{Client: remappedTestClient(server), Now: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := result.CandidateReceipt()
	if receipt.Release == nil || receipt.Release.Tag != "v1.2.3" || receipt.Release.TargetCommitish != "main" {
		t.Fatalf("unexpected release metadata: %#v", receipt.Release)
	}
	bodyNormalized, _ := normalizeText([]byte(secretBody))
	bodyHash := sha256.Sum256(bodyNormalized)
	if receipt.Release.BodySHA256 != hex.EncodeToString(bodyHash[:]) {
		t.Fatalf("body hash = %s", receipt.Release.BodySHA256)
	}
	encoded, err := EncodeReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range [][]byte{result.SemanticContent, encoded} {
		if strings.Contains(string(candidate), "Release notes") {
			t.Fatalf("canonical output leaked release body: %s", candidate)
		}
	}
	if !strings.Contains(string(result.SemanticContent), `"body_sha256"`) {
		t.Fatalf("canonical extraction omitted body hash: %s", result.SemanticContent)
	}
}

func testFeed(id, url string, kind FeedKind) Feed {
	return Feed{ID: id, URL: url, Kind: kind, CadenceHours: 24}
}

func refreshTestFeed(id string, kind FeedKind) Feed {
	var source string
	switch kind {
	case KindMarkdown:
		source = "https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/docs/protocol.md"
	case KindText:
		source = "https://raw.githubusercontent.com/modelcontextprotocol/modelcontextprotocol/2026-07-28/schema/2026-07-28/schema.json"
	case KindHTML:
		source = "https://learn.chatgpt.com/docs/changelog"
	case KindGitHubLatestRelease:
		source = "https://api.github.com/repos/openai/codex/releases/latest"
	default:
		panic("unsupported test feed kind")
	}
	return testFeed(id, source, kind)
}

func remappedTestClient(server *httptest.Server) *http.Client {
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	serverAddress := server.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", serverAddress)
	}
	client.Transport = transport
	return client
}

func fixedTime() time.Time {
	return time.Date(2026, 8, 11, 20, 0, 0, 0, time.UTC)
}
