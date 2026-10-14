package main

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed templates.html static/*
var assets embed.FS
var templates = template.Must(template.ParseFS(assets, "templates.html"))

type Word struct {
	ID                             int64
	English, French, Group, Gender string
}
type Page struct {
	Words, AllWords                                  []Word
	Groups                                           []string
	Filter                                           string
	Card                                             *Word
	Editor                                           *Word
	EditorError                                      string
	Direction, Mode, Prompt, Answer, Feedback, Error string
	Correct, Checked                                 bool
	Total, Attempts, Success                         int
}
type App struct{ db *sql.DB }

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS words (id INTEGER PRIMARY KEY, english TEXT NOT NULL COLLATE NOCASE, french TEXT NOT NULL COLLATE NOCASE, UNIQUE(english,french)); CREATE TABLE IF NOT EXISTS attempts (id INTEGER PRIMARY KEY, word_id INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE, correct INTEGER NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	// Add optional metadata without changing existing words or practice history.
	for _, column := range []string{"word_group", "gender"} {
		var present int
		if err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('words') WHERE name=?", column).Scan(&present); err == nil && present == 0 {
			definition := "TEXT NOT NULL DEFAULT ''"
			if column == "gender" {
				definition += " CHECK(gender IN ('','masculine','feminine'))"
			}
			_, err = db.Exec("ALTER TABLE words ADD COLUMN " + column + " " + definition)
		}
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}
func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(s, "’", "'")), " "))
}

// Semicolons separate alternatives; commas remain part of a phrase.
func alternatives(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ";") {
		part = strings.Join(strings.Fields(part), " ")
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
func cleanTranslations(s string) (string, error) {
	var result []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(s, ";") {
		part = strings.Join(strings.Fields(part), " ")
		if part == "" {
			return "", fmt.Errorf("Enter a translation between each semicolon.")
		}
		key := normalize(part)
		if !seen[key] {
			result = append(result, part)
			seen[key] = true
		}
	}
	value := strings.Join(result, "; ")
	if len([]rune(value)) > 200 {
		return "", fmt.Errorf("Keep each language’s translations within 200 characters.")
	}
	return value, nil
}
func accepts(answer, translations string) bool {
	for _, translation := range alternatives(translations) {
		if normalize(answer) == normalize(translation) {
			return true
		}
	}
	return false
}
func firstTranslation(translations string) string {
	parts := alternatives(translations)
	if len(parts) == 0 {
		return translations
	}
	return parts[0]
}
func direction(r *http.Request) string {
	d := r.FormValue("direction")
	if d != "en-fr" && d != "mixed" {
		return "fr-en"
	}
	return d
}
func (a *App) page(r *http.Request) (Page, error) {
	p := Page{Direction: direction(r), Mode: direction(r), Filter: r.FormValue("filter")}
	if r.FormValue("mode") == "mixed" {
		p.Mode = "mixed"
	}
	rows, err := a.db.Query("SELECT id,english,french,word_group,gender FROM words ORDER BY english COLLATE NOCASE")
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var w Word
		if err = rows.Scan(&w.ID, &w.English, &w.French, &w.Group, &w.Gender); err != nil {
			return p, err
		}
		p.AllWords = append(p.AllWords, w)
		if p.Filter == "" || (p.Filter == "ungrouped" && w.Group == "") || p.Filter == "group:"+w.Group {
			p.Words = append(p.Words, w)
		}
	}
	if err = rows.Err(); err != nil {
		return p, err
	}
	rows.Close()
	p.Total = len(p.AllWords)
	seen := make(map[string]bool)
	for _, word := range p.AllWords {
		if word.Group != "" && !seen[word.Group] {
			p.Groups = append(p.Groups, word.Group)
			seen[word.Group] = true
		}
	}
	sort.Slice(p.Groups, func(i, j int) bool { return strings.ToLower(p.Groups[i]) < strings.ToLower(p.Groups[j]) })
	err = a.db.QueryRow("SELECT COUNT(*),COALESCE(SUM(correct),0) FROM attempts").Scan(&p.Attempts, &p.Success)
	return p, err
}
func (a *App) card(p *Page, exclude string) error {
	var candidates []Word
	for _, word := range p.Words {
		if strconv.FormatInt(word.ID, 10) != exclude {
			candidates = append(candidates, word)
		}
	}
	if len(candidates) == 0 {
		candidates = p.Words
	}
	if len(candidates) == 0 {
		return nil
	}
	w := candidates[rand.IntN(len(candidates))]
	p.Card = &w
	if p.Direction == "mixed" {
		if time.Now().UnixNano()%2 == 0 {
			p.Direction = "en-fr"
		} else {
			p.Direction = "fr-en"
		}
	}
	if p.Direction == "en-fr" {
		p.Prompt = firstTranslation(w.English)
	} else {
		p.Prompt = firstTranslation(w.French)
	}
	return nil
}
func cleanGroup(value string, groups []string) (string, error) {
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) > 80 {
		return "", fmt.Errorf("Keep group names within 80 characters.")
	}
	for _, group := range groups {
		if normalize(value) == normalize(group) {
			return group, nil
		}
	}
	return value, nil
}

func validGender(value string) bool {
	return value == "" || value == "masculine" || value == "feminine"
}

func (a *App) render(w http.ResponseWriter, r *http.Request, p Page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	name := "page"
	if r.Header.Get("HX-Request") == "true" {
		name = "workspace"
	}
	if err := templates.ExecuteTemplate(w, name, p); err != nil {
		log.Print(err)
	}
}
func (a *App) serve(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	p, err := a.page(r)
	if err != nil {
		http.Error(w, "Database unavailable", 500)
		log.Print(err)
		return
	}
	switch r.URL.Path {
	case "/":
		err = a.card(&p, r.FormValue("previous"))
	case "/words":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		en, enErr := cleanTranslations(r.FormValue("english"))
		fr, frErr := cleanTranslations(r.FormValue("french"))
		group, groupErr := cleanGroup(r.FormValue("group"), p.Groups)
		gender := r.FormValue("gender")
		if enErr != nil {
			p.Error = enErr.Error()
		} else if frErr != nil {
			p.Error = frErr.Error()
		} else if groupErr != nil {
			p.Error = groupErr.Error()
		} else if !validGender(gender) {
			p.Error = "Choose masculine, feminine, or leave noun gender unspecified."
		} else {
			for _, existing := range p.AllWords {
				if normalize(existing.English) == normalize(en) && normalize(existing.French) == normalize(fr) {
					p.Error = "That word pair is already in your collection."
					if err = a.card(&p, ""); err != nil {
						http.Error(w, "Database unavailable", 500)
						return
					}
					a.render(w, r, p)
					return
				}
			}
			result, e := a.db.Exec("INSERT INTO words(english,french,word_group,gender) VALUES(?,?,?,?) ON CONFLICT DO NOTHING", en, fr, group, gender)
			if e != nil {
				err = e
			} else {
				n, _ := result.RowsAffected()
				if n == 0 {
					p.Error = "That word pair is already in your collection."
				} else {
					p.Feedback = "Word added. Ready to practise!"
				}
			}
		}
		if err == nil {
			fresh, e := a.page(r)
			err = e
			fresh.Error = p.Error
			fresh.Feedback = p.Feedback
			p = fresh
			if err == nil {
				err = a.card(&p, "")
			}
		}
	case "/edit":
		id, parseErr := strconv.ParseInt(r.FormValue("id"), 10, 64)
		var original *Word
		if parseErr == nil {
			for _, word := range p.AllWords {
				if word.ID == id {
					copy := word
					original = &copy
					break
				}
			}
		}
		if original == nil {
			http.Error(w, "This word no longer exists.", 404)
			return
		}
		if r.Method == "GET" {
			p.Editor = original
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				if e := templates.ExecuteTemplate(w, "editor", p); e != nil {
					log.Print(e)
				}
				return
			}
		} else if r.Method == "POST" {
			submitted := Word{ID: id, English: r.FormValue("english"), French: r.FormValue("french"), Group: r.FormValue("group"), Gender: r.FormValue("gender")}
			en, enErr := cleanTranslations(submitted.English)
			fr, frErr := cleanTranslations(submitted.French)
			group, groupErr := cleanGroup(submitted.Group, p.Groups)
			switch {
			case enErr != nil:
				p.EditorError = enErr.Error()
			case frErr != nil:
				p.EditorError = frErr.Error()
			case groupErr != nil:
				p.EditorError = groupErr.Error()
			case !validGender(submitted.Gender):
				p.EditorError = "Choose masculine, feminine, or leave noun gender unspecified."
			}
			if p.EditorError == "" {
				for _, word := range p.AllWords {
					if word.ID != id && normalize(word.English) == normalize(en) && normalize(word.French) == normalize(fr) {
						p.EditorError = "That word pair is already in your collection."
						break
					}
				}
			}
			if p.EditorError != "" {
				p.Editor = &submitted
			} else {
				_, err = a.db.Exec("UPDATE words SET english=?,french=?,word_group=?,gender=? WHERE id=?", en, fr, group, submitted.Gender, id)
				if err == nil {
					p, err = a.page(r)
					p.Feedback = "Word updated."
				}
			}
		} else {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if err == nil {
			err = a.card(&p, "")
		}
	case "/gender":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		gender := r.FormValue("gender")
		if !validGender(gender) {
			p.Error = "Choose masculine, feminine, or leave noun gender unspecified."
		} else {
			var result sql.Result
			result, err = a.db.Exec("UPDATE words SET gender=? WHERE id=?", gender, r.FormValue("id"))
			if err == nil {
				n, e := result.RowsAffected()
				err = e
				if err == nil && n == 0 {
					http.Error(w, "This word no longer exists.", 404)
					return
				}
			}
			if err == nil {
				p, err = a.page(r)
				p.Feedback = "Noun gender saved."
			}
		}
		if err == nil {
			err = a.card(&p, "")
		}
	case "/group":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		group, groupErr := cleanGroup(r.FormValue("group"), p.Groups)
		if groupErr != nil {
			p.Error = groupErr.Error()
		} else {
			var result sql.Result
			result, err = a.db.Exec("UPDATE words SET word_group=? WHERE id=?", group, r.FormValue("id"))
			if err == nil {
				n, e := result.RowsAffected()
				err = e
				if err == nil && n == 0 {
					http.Error(w, "This word no longer exists.", 404)
					return
				}
			}
			if err == nil {
				p, err = a.page(r)
				p.Feedback = "Group saved."
			}
		}
		if err == nil {
			err = a.card(&p, "")
		}
	case "/delete":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		_, err = a.db.Exec("DELETE FROM words WHERE id=?", r.FormValue("id"))
		if err == nil {
			p, err = a.page(r)
		}
		if err == nil {
			err = a.card(&p, "")
		}
	case "/answer":
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		var word Word
		err = a.db.QueryRow("SELECT id,english,french,word_group,gender FROM words WHERE id=?", r.FormValue("id")).Scan(&word.ID, &word.English, &word.French, &word.Group, &word.Gender)
		if err == sql.ErrNoRows {
			http.Error(w, "This word no longer exists. Refresh to continue.", 404)
			return
		}
		if err == nil {
			p.Card = &word
			p.Checked = true
			p.Answer = word.English
			p.Prompt = firstTranslation(word.French)
			if p.Direction == "en-fr" {
				p.Answer = word.French
				p.Prompt = firstTranslation(word.English)
			}
			p.Correct = accepts(r.FormValue("answer"), p.Answer)
			p.Answer = strings.Join(alternatives(p.Answer), " or ")
			if strings.TrimSpace(r.FormValue("answer")) == "" {
				p.Checked = false
				p.Error = "Enter a translation before checking."
			} else {
				_, err = a.db.Exec("INSERT INTO attempts(word_id,correct) VALUES(?,?)", word.ID, p.Correct)
				p.Attempts++
				if p.Correct {
					p.Success++
				}
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Print(err)
		http.Error(w, "Something went wrong. Please try again.", 500)
		return
	}
	a.render(w, r, p)
}
func main() {
	path := os.Getenv("DATABASE_PATH")
	if path == "" {
		path = "french.db"
	}
	db, err := openDB(path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	app := &App{db: db}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.FileServer(http.FS(assets)))
	mux.HandleFunc("/", app.serve)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if _, err := strconv.Atoi(port); err != nil {
		log.Fatal("PORT must be numeric")
	}
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("French practice ready at http://localhost:%s", port)
	log.Fatal(server.ListenAndServe())
}
