package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

func TestLatestSelectsHighestPublishedVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2&per_page=100>; rel="next"`, "http://"+r.Host+r.URL.Path))
			fmt.Fprint(w, `[{"tag_name":"v1.1.0","assets":[]},{"tag_name":"v2.0.0","draft":true}]`)
			return
		}
		fmt.Fprint(w, `[{"tag_name":"v1.2.0-rc.1","prerelease":true,"assets":[{"name":"herdr-tg_linux_amd64","state":"uploaded","browser_download_url":"https://github.com/bin"},{"name":"checksums.txt","state":"uploaded","browser_download_url":"https://github.com/sums"}]},{"tag_name":"bad"}]`)
	}))
	defer server.Close()
	s := &Source{Client: server.Client(), URL: server.URL + "/releases", OS: "linux", Arch: "amd64"}
	// A stable installation is never offered a prerelease.
	installed, _ := domain.ParseVersion("1.0.0")
	rel, found, err := s.Latest(context.Background(), installed)
	if err != nil || !found || rel.Tag != "v1.1.0" {
		t.Fatalf("stable: release=%+v found=%v err=%v", rel, found, err)
	}
	// An installed prerelease opted into testing and may move to the next.
	installed, _ = domain.ParseVersion("1.0.0-rc.1")
	rel, found, err = s.Latest(context.Background(), installed)
	if err != nil || !found || rel.Tag != "v1.2.0-rc.1" || rel.AssetURL == "" || rel.ChecksumsURL == "" {
		t.Fatalf("prerelease: release=%+v found=%v err=%v", rel, found, err)
	}
}

func TestLatestFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"rate limited", 403, `{}`}, {"invalid body", 200, `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			s := &Source{Client: server.Client(), URL: server.URL}
			v, _ := domain.ParseVersion("1.0.0")
			if _, _, err := s.Latest(context.Background(), v); err == nil {
				t.Fatal("expected scan failure")
			}
		})
	}
}

func TestLatestRejectsCrossHostPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<https://example.com/steal>; rel="next"`)
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	s := &Source{Client: server.Client(), URL: server.URL}
	v, _ := domain.ParseVersion("1.0.0")
	_, _, err := s.Latest(context.Background(), v)
	if err == nil || !strings.Contains(err.Error(), "changed endpoint") {
		t.Fatalf("err=%v", err)
	}
}

func TestChecksumRequiresExactUniqueAsset(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"valid", strings.Repeat("a", 64) + "  herdr-tg_linux_amd64\n", true},
		{"wrong asset", strings.Repeat("a", 64) + "  herdr-tg_linux_arm64\n", false},
		{"duplicate", strings.Repeat("a", 64) + "  herdr-tg_linux_amd64\n" + strings.Repeat("b", 64) + "  herdr-tg_linux_amd64\n", false},
		{"bad digest", "abc  herdr-tg_linux_amd64\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			s := &Source{Client: server.Client(), OS: "linux", Arch: "amd64", CheckURL: allowAnyURL}
			got, err := s.Checksum(context.Background(), domain.Release{Tag: "v1.2.0", AssetURL: "https://example.com/bin", ChecksumsURL: server.URL})
			if (err == nil) != tc.ok {
				t.Fatalf("digest=%q err=%v", got, err)
			}
		})
	}
}

func allowAnyURL(*url.URL) error { return nil }

// TestReleaseURLsAllowList: asset and checksum URLs come from API JSON; only
// https on GitHub's own hosts may be fetched or handed to the installer.
func TestReleaseURLsAllowList(t *testing.T) {
	for _, tc := range []struct {
		url string
		ok  bool
	}{
		{"https://github.com/permgps/herdr-telegram-agents/releases/download/v1.2.0/checksums.txt", true},
		{"http://github.com/permgps/herdr-telegram-agents/releases/download/v1.2.0/checksums.txt", false},
		{"https://evil.example/checksums.txt", false},
		{"https://github.com.evil.example/x", false},
		{"https://user@github.com/x", false},
	} {
		s := &Source{Client: http.DefaultClient, OS: "linux", Arch: "amd64"}
		_, err := s.Checksum(context.Background(), domain.Release{Tag: "v1.2.0", AssetURL: tc.url, ChecksumsURL: tc.url})
		rejected := err != nil && strings.Contains(err.Error(), "not allowed")
		if rejected == tc.ok {
			t.Fatalf("%s: err = %v", tc.url, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"v1.2.0","assets":[{"name":"herdr-tg_linux_amd64","state":"uploaded","browser_download_url":"http://evil.example/bin"},{"name":"checksums.txt","state":"uploaded","browser_download_url":"https://github.com/sums"}]}]`)
	}))
	defer server.Close()
	s := &Source{Client: server.Client(), URL: server.URL, OS: "linux", Arch: "amd64"}
	installed, _ := domain.ParseVersion("1.0.0")
	rel, found, err := s.Latest(context.Background(), installed)
	if err != nil || !found || rel.AssetURL != "" {
		t.Fatalf("foreign asset URL kept: %+v %v %v", rel, found, err)
	}
}

// TestReleaseClientRedirectAllowList: GitHub redirects downloads to its
// asset hosts; any other target or scheme ends the request.
func TestReleaseClientRedirectAllowList(t *testing.T) {
	c := NewHTTPClient()
	check := func(target string, hops int) error {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		return c.CheckRedirect(req, make([]*http.Request, hops))
	}
	if err := check("https://release-assets.githubusercontent.com/x", 1); err != nil {
		t.Fatal(err)
	}
	if err := check("https://objects.githubusercontent.com/x", 1); err != nil {
		t.Fatal(err)
	}
	if check("https://evil.example/x", 1) == nil || check("http://objects.githubusercontent.com/x", 1) == nil || check("https://github.com/x", 5) == nil {
		t.Fatal("redirect allow-list not enforced")
	}
}
