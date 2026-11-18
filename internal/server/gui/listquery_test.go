package gui

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseListQuery_RejectsUnknownSort(t *testing.T) {
	q := url.Values{"sort": {"password_hash; DROP TABLE users"}, "dir": {"sideways"}}
	lq := parseListQuery(q, []string{"domain", "requests"}, "requests", "desc")
	if lq.Sort != "requests" || lq.Dir != "desc" {
		t.Fatalf("got sort=%q dir=%q, want defaults", lq.Sort, lq.Dir)
	}
	if lq.Page != 1 {
		t.Fatalf("page = %d, want 1", lq.Page)
	}
}

func TestListQuery_PageHrefKeepsFiltersAndSort(t *testing.T) {
	q := url.Values{"period": {"month"}, "sort": {"domain"}, "dir": {"asc"}, "page": {"2"}, "junk": {"x"}}
	lq := parseListQuery(q, []string{"domain", "requests"}, "requests", "desc", "period")
	got, err := url.ParseQuery(strings.TrimPrefix(lq.PageHref(3), "?"))
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"period": {"month"}, "sort": {"domain"}, "dir": {"asc"}, "page": {"3"}}
	if got.Encode() != want.Encode() {
		t.Fatalf("PageHref = %q, want %q", got.Encode(), want.Encode())
	}
}

func TestListQuery_SortHrefTogglesAndResetsPage(t *testing.T) {
	q := url.Values{"sort": {"domain"}, "dir": {"asc"}, "page": {"4"}}
	lq := parseListQuery(q, []string{"domain", "requests"}, "requests", "desc").withDescFirst("requests")

	if got := lq.SortHref("domain"); got != "?dir=desc&sort=domain" {
		t.Fatalf("active column should flip direction, got %q", got)
	}

	if got := lq.SortHref("requests"); got != "?" {
		t.Fatalf("default sort should produce a bare link, got %q", got)
	}
	if lq.SortState("domain") != "asc" || lq.SortState("requests") != "" {
		t.Fatal("unexpected SortState")
	}
}

func TestListQuery_PaginateClampsPage(t *testing.T) {
	lq := parseListQuery(url.Values{"page": {"99"}}, []string{"id"}, "id", "asc")
	offset, data := lq.paginate(45, 20)
	if lq.Page != 3 || offset != 40 {
		t.Fatalf("page=%d offset=%d, want 3/40", lq.Page, offset)
	}
	if data["HasNext"].(bool) || !data["HasPrev"].(bool) {
		t.Fatal("bad HasPrev/HasNext")
	}
}

func TestRender_StatsPaginationHrefIsNotEscaped(t *testing.T) {
	tpl, err := loadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	lq := parseListQuery(url.Values{"period": {"month"}, "page": {"2"}}, []string{"requests"}, "requests", "desc", "period")
	data := pageData()
	_, pg := lq.paginate(100, 20)
	for k, v := range pg {
		data[k] = v
	}
	data["Q"] = lq
	data["Period"] = "month"
	var sb strings.Builder
	if err := tpl.render(&sb, "stats.html", "en", data); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, `href="?page=3&amp;period=month"`) {
		t.Fatalf("next-page link missing or escaped:\n%s", out)
	}
	if strings.Contains(out, "%26") {
		t.Fatal("found percent-escaped ampersand in output")
	}
}
