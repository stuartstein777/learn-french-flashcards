package main

import (
	"database/sql"
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

func TestGroupsAndFilteredPractice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "groups.db")
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
	request("/words", url.Values{"english": {"shirt"}, "french": {"la chemise"}, "group": {" Clothing "}})
	request("/words", url.Values{"english": {"hat"}, "french": {"le chapeau"}, "group": {"clothing"}})
	request("/words", url.Values{"english": {"apple"}, "french": {"la pomme"}, "group": {"Food"}})
	request("/words", url.Values{"english": {"well; good"}, "french": {"bien"}})
	db.Close()
	db, err = openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	app.db = db
	page := func(filter string) Page {
		t.Helper()
		r := httptest.NewRequest("GET", "/?"+url.Values{"filter": {filter}}.Encode(), nil)
		p, e := app.page(r)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	p := page("group:Clothing")
	if len(p.Words) != 2 || len(p.Groups) != 2 || p.Total != 4 {
		t.Fatalf("filtered page: %+v", p)
	}
	for i := 0; i < 30; i++ {
		if err := app.card(&p, "1"); err != nil {
			t.Fatal(err)
		}
		if p.Card == nil || p.Card.ID != 2 {
			t.Fatalf("filter/exclusion ignored: %+v", p.Card)
		}
	}
	food := page("group:Food")
	app.card(&food, "3")
	if food.Card == nil || food.Card.ID != 3 {
		t.Fatal("single-word filter fell back outside group")
	}
	empty := page("group:Greetings")
	app.card(&empty, "")
	if empty.Card != nil {
		t.Fatal("empty group fell back to all words")
	}
	ungrouped := page("ungrouped")
	app.card(&ungrouped, "")
	if ungrouped.Card == nil || ungrouped.Card.ID != 4 {
		t.Fatal("ungrouped filter failed")
	}
	body := request("/answer", url.Values{"id": {"1"}, "answer": {"shirt"}, "filter": {"group:Clothing"}, "mode": {"mixed"}, "direction": {"fr-en"}})
	if !strings.Contains(body, "That’s correct") || !strings.Contains(body, `name="filter" value="group:Clothing"`) || !strings.Contains(body, `name="direction" value="mixed"`) {
		t.Fatal("answer lost filter or mixed mode")
	}
	body = request("/group", url.Values{"id": {"4"}, "group": {" greetings "}})
	if !strings.Contains(body, `<option value="greetings">`) {
		t.Fatal("new group suggestion missing")
	}
	request("/group", url.Values{"id": {"1"}, "group": {"FOOD"}, "filter": {"group:Clothing"}})
	if p = page("group:Food"); len(p.Words) != 2 {
		t.Fatal("existing group not reused")
	}
	request("/group", url.Values{"id": {"1"}, "group": {""}})
	if p = page("ungrouped"); len(p.Words) != 1 || p.Words[0].ID != 1 {
		t.Fatal("clearing group failed")
	}
	body = request("/group", url.Values{"id": {"1"}, "group": {strings.Repeat("é", 81)}})
	if !strings.Contains(body, "Keep group names within 80 characters.") {
		t.Fatal("group length not validated")
	}
	request("/words", url.Values{"english": {"apple"}, "french": {"la pomme"}, "group": {"Clothing"}, "filter": {"group:Clothing"}})
	var count int
	db.QueryRow("SELECT COUNT(*) FROM words").Scan(&count)
	if count != 4 {
		t.Fatal("duplicate word inserted from filtered view")
	}
	var attempts int
	db.QueryRow("SELECT COUNT(*) FROM attempts WHERE word_id=1").Scan(&attempts)
	if attempts != 1 {
		t.Fatal("group edits removed practice history")
	}
}

func TestLegacyDatabaseGroupMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE words (id INTEGER PRIMARY KEY, english TEXT NOT NULL COLLATE NOCASE, french TEXT NOT NULL COLLATE NOCASE, UNIQUE(english,french)); CREATE TABLE attempts (id INTEGER PRIMARY KEY, word_id INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE, correct INTEGER NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP); INSERT INTO words VALUES(1,'hello','bonjour'); INSERT INTO attempts(word_id,correct) VALUES(1,1);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	for i := 0; i < 2; i++ {
		db, err = openDB(path)
		if err != nil {
			t.Fatal(err)
		}
		p, e := (&App{db: db}).page(httptest.NewRequest("GET", "/", nil))
		if e != nil || p.Total != 1 || p.Attempts != 1 || p.Success != 1 || p.Words[0].Group != "" || p.Words[0].French != "bonjour" {
			t.Fatalf("migration lost data: %+v %v", p, e)
		}
		db.Close()
	}
}

func TestNounGender(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gender.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	app := &App{db: db}
	request := func(route string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", route, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		app.serve(w, r)
		return w
	}
	for _, tc := range []struct{ english, french, gender string }{{"book", "le livre", "masculine"}, {"house", "la maison", "feminine"}, {"well; good", "bien", ""}} {
		w := request("/words", url.Values{"english": {tc.english}, "french": {tc.french}, "gender": {tc.gender}, "group": {"Basics"}})
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	db.Close()
	db, err = openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	app.db = db
	for _, tc := range []struct{ id, gender, answer string }{{"1", "masculine", "book"}, {"2", "feminine", "house"}, {"3", "", "good"}} {
		var gender string
		if err := db.QueryRow("SELECT gender FROM words WHERE id=?", tc.id).Scan(&gender); err != nil || gender != tc.gender {
			t.Fatalf("gender not persisted: %q %v", gender, err)
		}
		w := request("/answer", url.Values{"id": {tc.id}, "answer": {tc.answer}, "filter": {"group:Basics"}, "direction": {"fr-en"}})
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "That’s correct") {
			t.Fatal("gender changed answer checking")
		}
		if (tc.gender != "") != strings.Contains(body, `class="gender-badge"`) {
			t.Fatal("flashcard gender hint missing or unexpected")
		}
	}
	for _, gender := range []string{"masculine", "feminine", ""} {
		w := request("/gender", url.Values{"id": {"1"}, "gender": {gender}, "filter": {"group:Basics"}, "direction": {"mixed"}})
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Noun gender saved.") || !strings.Contains(w.Body.String(), `name="filter" value="group:Basics"`) {
			t.Fatal("gender update or filter failed")
		}
		var saved string
		db.QueryRow("SELECT gender FROM words WHERE id=1").Scan(&saved)
		if saved != gender {
			t.Fatal("gender edit not saved")
		}
	}
	w := request("/gender", url.Values{"id": {"2"}, "gender": {"invalid"}})
	if !strings.Contains(w.Body.String(), "Choose masculine, feminine") {
		t.Fatal("invalid gender accepted")
	}
	w = request("/words", url.Values{"english": {"hat"}, "french": {"le chapeau"}, "gender": {"invalid"}})
	if !strings.Contains(w.Body.String(), "Choose masculine, feminine") {
		t.Fatal("invalid gender accepted on creation")
	}
	if w = request("/gender", url.Values{"id": {"999"}, "gender": {"masculine"}}); w.Code != 404 {
		t.Fatal("missing word not handled")
	}
	var words, attempts int
	db.QueryRow("SELECT COUNT(*) FROM words").Scan(&words)
	db.QueryRow("SELECT COUNT(*) FROM attempts").Scan(&attempts)
	if words != 3 || attempts != 3 {
		t.Fatal("gender edits changed words or history")
	}
}

func TestGroupedDatabaseGenderMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grouped.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE words(id INTEGER PRIMARY KEY,english TEXT NOT NULL,french TEXT NOT NULL,word_group TEXT NOT NULL DEFAULT ''); INSERT INTO words VALUES(1,'house','la maison','Home'); CREATE TABLE attempts(id INTEGER PRIMARY KEY,word_id INTEGER NOT NULL REFERENCES words(id),correct INTEGER NOT NULL,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP); INSERT INTO attempts(word_id,correct) VALUES(1,1);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	for i := 0; i < 2; i++ {
		db, err = openDB(path)
		if err != nil {
			t.Fatal(err)
		}
		p, e := (&App{db: db}).page(httptest.NewRequest("GET", "/", nil))
		if e != nil || p.Total != 1 || p.Attempts != 1 || p.Success != 1 || p.Words[0].Group != "Home" || p.Words[0].Gender != "" || p.Words[0].French != "la maison" {
			t.Fatalf("migration lost data: %+v %v", p, e)
		}
		db.Close()
	}
}

func TestWordEditor(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "edit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec("INSERT INTO words(id,english,french,word_group,gender) VALUES(1,'book','le livre','Things','masculine'),(2,'hello','bonjour','Greetings','')")
	db.Exec("INSERT INTO attempts(word_id,correct) VALUES(1,1)")
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
	w := request("GET", "/", nil)
	if strings.Contains(w.Body.String(), "group-edit") || strings.Contains(w.Body.String(), "gender-edit") || !strings.Contains(w.Body.String(), ">Edit</a>") {
		t.Fatal("list still has inline controls or lacks Edit")
	}
	w = request("GET", "/edit?id=1&filter=group:Things&direction=mixed", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<dialog id="word-editor"`) || !strings.Contains(w.Body.String(), `value="le livre"`) || !strings.Contains(w.Body.String(), `value="masculine" selected`) {
		t.Fatal("editor not prefilled")
	}
	fields := url.Values{"id": {"1"}, "english": {"house; home"}, "french": {"la maison"}, "group": {" greetings "}, "gender": {"feminine"}, "filter": {"group:Things"}, "direction": {"mixed"}}
	w = request("POST", "/edit", fields)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Word updated.") || strings.Contains(w.Body.String(), `<dialog id="word-editor"`) {
		t.Fatal("save did not close editor")
	}
	var en, fr, group, gender string
	db.QueryRow("SELECT english,french,word_group,gender FROM words WHERE id=1").Scan(&en, &fr, &group, &gender)
	if en != "house; home" || fr != "la maison" || group != "Greetings" || gender != "feminine" {
		t.Fatalf("fields not saved: %q %q %q %q", en, fr, group, gender)
	}
	for _, tc := range []struct{ key, value, message string }{{"english", "hello", "already in your collection"}, {"english", ";home", "Enter a translation"}, {"group", strings.Repeat("x", 81), "Keep group names"}, {"gender", "invalid", "Choose masculine"}} {
		bad := url.Values{}
		for k, v := range fields {
			bad[k] = append([]string(nil), v...)
		}
		bad.Set(tc.key, tc.value)
		if tc.message == "already in your collection" {
			bad.Set("french", "bonjour")
		}
		w = request("POST", "/edit", bad)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.message) || !strings.Contains(w.Body.String(), `<dialog id="word-editor"`) {
			t.Fatalf("validation didn't retain editor: %s", w.Body.String())
		}
		db.QueryRow("SELECT english FROM words WHERE id=1").Scan(&en)
		if en != "house; home" {
			t.Fatal("invalid edit changed database")
		}
	}
	var attempts int
	db.QueryRow("SELECT COUNT(*) FROM attempts WHERE word_id=1").Scan(&attempts)
	if attempts != 1 {
		t.Fatal("editing removed history")
	}
	fields.Set("group", "")
	fields.Set("gender", "")
	w = request("POST", "/edit", fields)
	if w.Code != 200 {
		t.Fatal("clearing metadata failed")
	}
	db.QueryRow("SELECT word_group,gender FROM words WHERE id=1").Scan(&group, &gender)
	if group != "" || gender != "" {
		t.Fatal("metadata not cleared")
	}
	if w = request("GET", "/edit?id=999", nil); w.Code != 404 {
		t.Fatal("missing word not handled")
	}
	if w = request("PUT", "/edit?id=1", nil); w.Code != 405 {
		t.Fatal("unexpected method accepted")
	}
}
