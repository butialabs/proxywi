package gui

import (
	"net/url"
	"slices"
	"strconv"
)

type listQuery struct {
	filters url.Values
	Sort    string
	Dir     string // "asc" | "desc"
	Page    int

	defSort string
	defDir  string

	dirFor map[string]string
}

func parseListQuery(q url.Values, allowed []string, defSort, defDir string, keep ...string) listQuery {
	lq := listQuery{filters: url.Values{}, defSort: defSort, defDir: defDir}
	for _, k := range keep {
		if v := q.Get(k); v != "" {
			lq.filters.Set(k, v)
		}
	}
	lq.Sort = q.Get("sort")
	if !slices.Contains(allowed, lq.Sort) {
		lq.Sort = defSort
	}
	switch q.Get("dir") {
	case "asc", "desc":
		lq.Dir = q.Get("dir")
	default:
		if lq.Sort == defSort {
			lq.Dir = defDir
		} else {
			lq.Dir = "asc"
		}
	}
	lq.Page, _ = strconv.Atoi(q.Get("page"))
	if lq.Page < 1 {
		lq.Page = 1
	}
	return lq
}

func (lq listQuery) withDescFirst(keys ...string) listQuery {
	lq.dirFor = make(map[string]string, len(keys))
	for _, k := range keys {
		lq.dirFor[k] = "desc"
	}
	return lq
}

func (lq listQuery) Desc() bool { return lq.Dir == "desc" }

func (lq listQuery) IsDefault() bool { return lq.Sort == lq.defSort && lq.Dir == lq.defDir }

func (lq listQuery) values(sort, dir string, page int) url.Values {
	v := url.Values{}
	for k, vals := range lq.filters {
		v[k] = append([]string(nil), vals...)
	}
	if sort != lq.defSort || dir != lq.defDir {
		v.Set("sort", sort)
		v.Set("dir", dir)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	return v
}

func encodeHref(v url.Values) string {
	if enc := v.Encode(); enc != "" {
		return "?" + enc
	}
	return "?"
}

func (lq listQuery) PageHref(n int) string {
	return encodeHref(lq.values(lq.Sort, lq.Dir, n))
}

func (lq listQuery) SortHref(key string) string {
	dir := "asc"
	if d, ok := lq.dirFor[key]; ok {
		dir = d
	}
	if key == lq.Sort {
		if lq.Dir == "asc" {
			dir = "desc"
		} else {
			dir = "asc"
		}
	}
	return encodeHref(lq.values(key, dir, 1))
}

func (lq listQuery) FilterHref(key, val string) string {
	v := lq.values(lq.Sort, lq.Dir, 1)
	v.Set(key, val)
	return encodeHref(v)
}

func (lq listQuery) SortState(key string) string {
	if key == lq.Sort {
		return lq.Dir
	}
	return ""
}

func (lq *listQuery) paginate(total, pageSize int) (offset int, data map[string]any) {
	totalPages := max((total+pageSize-1)/pageSize, 1)
	if lq.Page > totalPages {
		lq.Page = totalPages
	}
	return (lq.Page - 1) * pageSize, map[string]any{
		"Page":       lq.Page,
		"TotalPages": totalPages,
		"Total":      total,
		"HasPrev":    lq.Page > 1,
		"HasNext":    lq.Page < totalPages,
		"PrevHref":   lq.PageHref(lq.Page - 1),
		"NextHref":   lq.PageHref(lq.Page + 1),
	}
}
