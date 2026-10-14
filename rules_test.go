package main

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestRulesNotebook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notebook.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	app := &App{db: db}
	request := func(method, route string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, route, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		app.serve(w, r)
		return w
	}
	w := request("GET", "/rules", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Add your first rule") {
		t.Fatal("empty notebook missing")
	}
	w = request("GET", "/rules/edit", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<dialog id="rule-editor"`) || !strings.Contains(w.Body.String(), "Add a rule") {
		t.Fatal("new editor missing")
	}
	fields := url.Values{"title": {"Je becomes j’"}, "explanation": {"Before a vowel or silent h, shorten je to j’.\nKeep the apostrophe."}, "correct_example": {"J’habite à Paris."}, "incorrect_example": {"Je habite à Paris."}, "notes": {"The apostrophe replaces e."}, "topic": {" Elision "}}
	w = request("POST", "/rules/edit", fields)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Rule saved.") || !strings.Contains(w.Body.String(), "<details>") || strings.Contains(w.Body.String(), `<dialog id="rule-editor"`) {
		t.Fatal("create or editor close failed")
	}
	request("POST", "/rules/edit", url.Values{"title": {"Another rule"}, "explanation": {"A pattern."}, "topic": {"elision"}})
	request("POST", "/rules/edit", url.Values{"title": {"Nouns"}, "explanation": {"A reminder."}, "topic": {"Gender"}})
	request("POST", "/rules/edit", url.Values{"title": {"No topic yet"}, "explanation": {"Notes."}})
	db.Close()
	db, err = openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	app.db = db
	page := func(query, topic string) RulesPage {
		t.Helper()
		r := httptest.NewRequest("GET", "/rules?"+url.Values{"q": {query}, "topic_filter": {topic}}.Encode(), nil)
		p, e := app.rulesPage(r)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	p := page("HABITE", "topic:Elision")
	if len(p.Rules) != 1 || p.Rules[0].CorrectExample != "J’habite à Paris." || len(p.Topics) != 2 || len(p.AllRules) != 4 {
		t.Fatalf("persist/search/topic mismatch: %+v", p)
	}
	for _, search := range []string{"vowel", "replaces", "je becomes j'"} {
		if len(page(search, "").Rules) != 1 {
			t.Fatalf("search failed: %s", search)
		}
	}
	if len(page("", "topic:Elision").Rules) != 2 || len(page("", "untopiced").Rules) != 1 || len(page("%", "").Rules) != 0 {
		t.Fatal("filtering or literal search failed")
	}
	w = request("GET", "/rules?q=missing", nil)
	if !strings.Contains(w.Body.String(), "No matching rules") {
		t.Fatal("no results missing")
	}
	w = request("GET", "/rules/edit?id=1&q=habite&topic_filter=topic:Elision", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `value="Je becomes j’"`) || !strings.Contains(w.Body.String(), `value="topic:Elision"`) {
		t.Fatal("prefill or filter preservation failed")
	}
	fields.Set("id", "1")
	fields.Set("title", "Elision reminder")
	fields.Set("topic", "GENDER")
	fields.Set("notes", "<script>alert(1)</script>")
	fields.Set("q", "habite")
	fields.Set("topic_filter", "topic:Elision")
	w = request("POST", "/rules/edit", fields)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Rule saved.") {
		t.Fatal("edit failed")
	}
	p = page("Elision reminder", "")
	if len(p.Rules) != 1 || p.Rules[0].Topic != "Gender" || p.Rules[0].Notes != "<script>alert(1)</script>" {
		t.Fatal("edit not stored")
	}
	w = request("GET", "/rules", nil)
	if strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("unsafe rendering")
	}
	for _, tc := range []struct{ field, value, message string }{{"title", " ", "title and an explanation"}, {"explanation", "", "title and an explanation"}, {"topic", strings.Repeat("é", 81), "topic names"}, {"title", strings.Repeat("x", 161), "title within 160"}} {
		invalid := url.Values{}
		for k, v := range fields {
			invalid[k] = append([]string(nil), v...)
		}
		invalid.Set(tc.field, tc.value)
		w = request("POST", "/rules/edit", invalid)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.message) || !strings.Contains(w.Body.String(), `<dialog id="rule-editor"`) {
			t.Fatalf("validation editor missing for %s", tc.field)
		}
		if len(page("Elision reminder", "").Rules) != 1 {
			t.Fatal("invalid edit overwrote saved rule")
		}
	}
	w = request("POST", "/rules/delete", url.Values{"id": {"1"}, "q": {"habite"}, "topic_filter": {"topic:Gender"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Rule removed.") {
		t.Fatal("delete failed")
	}
	if len(page("", "").AllRules) != 3 {
		t.Fatal("rule not deleted")
	}
	for _, route := range []string{"/rules/edit?id=999", "/rules/edit?id=oops"} {
		if w = request("GET", route, nil); w.Code != 404 {
			t.Fatalf("missing rule: %s", route)
		}
	}
	if w = request("GET", "/rules/delete?id=2", nil); w.Code != 405 {
		t.Fatal("GET deleted rule")
	}
	if w = request("POST", "/rules/delete", nil); w.Code != 400 {
		t.Fatal("missing id not rejected")
	}
	if w = request("POST", "/rules", nil); w.Code != 405 {
		t.Fatal("unexpected method")
	}
	if w = request("GET", "/rules/unknown", nil); w.Code != 404 {
		t.Fatal("unknown route")
	}
	var attempts, words int
	db.QueryRow("SELECT COUNT(*) FROM attempts").Scan(&attempts)
	db.QueryRow("SELECT COUNT(*) FROM words").Scan(&words)
	if attempts != 0 || words != 0 {
		t.Fatal("rules affected flashcard data")
	}
	r := httptest.NewRequest("GET", "/rules", nil)
	w = httptest.NewRecorder()
	app.serve(w, r)
	if !strings.Contains(w.Body.String(), "<!doctype html>") || !strings.Contains(w.Body.String(), `href="/rules" aria-current="page"`) {
		t.Fatal("full page or navigation missing")
	}
}
