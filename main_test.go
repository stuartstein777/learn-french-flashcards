package main

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestVocabularyPracticeAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	request := func(route string, values url.Values) string {
		t.Helper()
		r := httptest.NewRequest("POST", route, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		app.serve(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	request("/words", url.Values{"english": {"summer"}, "french": {"été"}})
	request("/words", url.Values{"english": {"SUMMER"}, "french": {"ÉTÉ"}})
	var count int
	db.QueryRow("SELECT COUNT(*) FROM words").Scan(&count)
	if count != 1 {
		t.Fatalf("duplicate count: %d", count)
	}
	for _, tc := range []struct{ dir, answer, message string }{{"en-fr", "  ÉTÉ  ", "That’s correct"}, {"en-fr", "ete", "Keep going"}, {"fr-en", "Summer", "That’s correct"}} {
		body := request("/answer", url.Values{"id": {"1"}, "direction": {tc.dir}, "answer": {tc.answer}})
		if !strings.Contains(body, tc.message) {
			t.Fatalf("unexpected feedback: %s", body)
		}
	}
	request("/answer", url.Values{"id": {"1"}, "answer": {" "}})
	var attempts, correct int
	db.QueryRow("SELECT COUNT(*),SUM(correct) FROM attempts").Scan(&attempts, &correct)
	if attempts != 3 || correct != 2 {
		t.Fatalf("stats %d/%d", correct, attempts)
	}
	db.Close()
	db, err = openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app.db = db
	p, err := app.page(httptest.NewRequest("GET", "/", nil))
	if err != nil || p.Total != 1 || p.Attempts != 3 {
		t.Fatalf("persistence: %+v %v", p, err)
	}
	request("/delete", url.Values{"id": {"1"}})
	db.QueryRow("SELECT COUNT(*) FROM attempts").Scan(&count)
	if count != 0 {
		t.Fatal("history not deleted")
	}
}
func TestEscapingAndEmptyDeck(t *testing.T) {
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &App{db: db}
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	a.serve(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Your first word awaits") {
		t.Fatal("empty state missing")
	}
	db.Exec("INSERT INTO words(english,french) VALUES(?,?)", "<script>alert(1)</script>", "bonjour")
	w = httptest.NewRecorder()
	a.serve(w, r)
	if strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("unescaped vocabulary")
	}
}

func TestAlternativeTranslations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alternatives.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	app := &App{db: db}
	request := func(route string, values url.Values) string {
		t.Helper()
		r := httptest.NewRequest("POST", route, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		app.serve(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	request("/words", url.Values{"english": {" well ; good ; WELL "}, "french": {"bien; très bien"}})
	var english, french string
	if err := db.QueryRow("SELECT english,french FROM words WHERE id=1").Scan(&english, &french); err != nil {
		t.Fatal(err)
	}
	if english != "well; good" || french != "bien; très bien" {
		t.Fatalf("saved alternatives: %q / %q", english, french)
	}
	db.Close()
	db, err = openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	app.db = db
	for _, tc := range []struct {
		direction, answer string
		correct           bool
	}{
		{"fr-en", "well", true}, {"fr-en", " GOOD ", true},
		{"fr-en", "well; good", false}, {"fr-en", "goo", false},
		{"en-fr", "bien", true}, {"en-fr", " TRÈS   BIEN ", true},
		{"en-fr", "tres bien", false},
	} {
		body := request("/answer", url.Values{"id": {"1"}, "direction": {tc.direction}, "answer": {tc.answer}})
		if strings.Contains(body, "That’s correct") != tc.correct {
			t.Fatalf("%s %q: incorrect feedback", tc.direction, tc.answer)
		}
		expected := "well or good"
		prompt := "bien"
		if tc.direction == "en-fr" {
			expected = "bien or très bien"
			prompt = "well"
		}
		if !strings.Contains(body, "<b>"+expected+"</b>") || !strings.Contains(body, ">"+prompt+"</h3>") {
			t.Fatalf("missing alternatives or prompt: %s", body)
		}
	}
	for _, invalid := range []string{";good", "good;", "good;;well", ";", " "} {
		body := request("/words", url.Values{"english": {invalid}, "french": {"bien"}})
		if !strings.Contains(body, "Enter a translation between each semicolon.") {
			t.Fatalf("accepted invalid alternatives %q", invalid)
		}
	}
	var count, attempts, correct int
	db.QueryRow("SELECT COUNT(*) FROM words").Scan(&count)
	db.QueryRow("SELECT COUNT(*),SUM(correct) FROM attempts").Scan(&attempts, &correct)
	if count != 1 || attempts != 7 || correct != 4 {
		t.Fatalf("words %d, attempts %d, correct %d", count, attempts, correct)
	}
	if !accepts("hello, friend", "hello, friend; hi") {
		t.Fatal("commas in phrases should be preserved")
	}
}
