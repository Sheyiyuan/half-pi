package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSearchFallsBackToDuckDuckGo(t *testing.T) {
	duckDuckGoFixture := `<div class="result"><a class="result__a" href="https://example.com/result">Fallback</a><a class="result__snippet">Found by fallback.</a></div>`
	var hosts []string
	client := newMemoryClient(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Hostname())
		if request.URL.Query().Get("q") != "half pi" {
			t.Errorf("query = %q", request.URL.Query().Get("q"))
		}
		if request.URL.Hostname() == "www.bing.com" {
			return memoryResponse(request, http.StatusOK, "text/html", "<html><body>No results</body></html>"), nil
		}
		return memoryResponse(request, http.StatusOK, "text/html", duckDuckGoFixture), nil
	})

	results, err := client.Search(context.Background(), "half pi", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "Fallback" {
		t.Fatalf("results = %+v", results)
	}
	if len(hosts) != 2 || hosts[0] != "www.bing.com" || hosts[1] != "html.duckduckgo.com" {
		t.Fatalf("provider order = %v", hosts)
	}
}

func TestParseDuckDuckGoResults(t *testing.T) {
	fixture := `<!doctype html><html><body>
<div class="result results_links">
  <h2><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Farticle%3Fx%3D1">Example <b>Article</b></a></h2>
  <a class="result__snippet">An example &amp; useful result.</a>
</div>
<div class="result results_links">
  <h2><a class="result__a" href="https://second.example/path#section">Second</a></h2>
  <a class="result__snippet">Second result.</a>
</div>
</body></html>`
	results, err := parseDuckDuckGoResults([]byte(fixture), "text/html; charset=utf-8", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Title != "Example Article" || results[0].URL != "https://example.com/article?x=1" || results[0].Snippet != "An example & useful result." {
		t.Fatalf("first result = %+v", results[0])
	}
	if results[1].URL != "https://second.example/path" {
		t.Fatalf("second result = %+v", results[1])
	}
}

func TestParseDuckDuckGoResultsHonorsLimitAndDeduplicates(t *testing.T) {
	fixture := `<div class="result"><a class="result__a" href="https://example.com">One</a></div>
<div class="result"><a class="result__a" href="https://example.com">Duplicate</a></div>
<div class="result"><a class="result__a" href="https://second.example">Two</a></div>`
	results, err := parseDuckDuckGoResults([]byte(fixture), "text/html", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "One" {
		t.Fatalf("results = %+v", results)
	}
}

func TestParseBingResults(t *testing.T) {
	fixture := `<ol id="b_results">
<li class="b_algo"><h2><a href="https://example.com/article">Example <strong>Article</strong></a></h2><div class="b_caption"><p>An example result.</p></div></li>
<li class="b_algo"><h2><a href="https://second.example/path#part">Second</a></h2><div class="b_caption"><p>Second result.</p></div></li>
</ol>`
	results, err := parseBingResults([]byte(fixture), "text/html; charset=utf-8", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Title != "Example Article" || results[0].Snippet != "An example result." || results[1].URL != "https://second.example/path" {
		t.Fatalf("results = %+v", results)
	}
}

func TestResolveResultURLRejectsNonHTTP(t *testing.T) {
	for _, href := range []string{"javascript:alert(1)", "mailto:test@example.com", ""} {
		if resolved := resolveDuckDuckGoURL(href); resolved != "" {
			t.Fatalf("href %q resolved to %q", href, resolved)
		}
	}
	if resolved := resolveDuckDuckGoURL("https://example.com/path#fragment"); strings.Contains(resolved, "#") {
		t.Fatalf("fragment was retained: %q", resolved)
	}
}
