package main

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type Rule struct {
	ID                                                                 int64
	Title, Explanation, CorrectExample, IncorrectExample, Notes, Topic string
}
type RulesPage struct {
	Rules, AllRules                           []Rule
	Topics                                    []string
	Query, TopicFilter, Feedback, EditorError string
	Editor                                    *Rule
}

func (a *App) rulesPage(r *http.Request) (RulesPage, error) {
	p := RulesPage{Query: r.FormValue("q"), TopicFilter: r.FormValue("topic_filter")}
	rows, err := a.db.Query("SELECT id,title,explanation,correct_example,incorrect_example,notes,topic FROM rules ORDER BY title COLLATE NOCASE,id")
	if err != nil {
		return p, err
	}
	defer rows.Close()
	topics := map[string]bool{}
	for rows.Next() {
		var rule Rule
		if err = rows.Scan(&rule.ID, &rule.Title, &rule.Explanation, &rule.CorrectExample, &rule.IncorrectExample, &rule.Notes, &rule.Topic); err != nil {
			return p, err
		}
		p.AllRules = append(p.AllRules, rule)
		if rule.Topic != "" && !topics[rule.Topic] {
			p.Topics = append(p.Topics, rule.Topic)
			topics[rule.Topic] = true
		}
		topicMatches := p.TopicFilter == "" || p.TopicFilter == "topic:"+rule.Topic || (p.TopicFilter == "untopiced" && rule.Topic == "")
		content := strings.Join([]string{rule.Title, rule.Explanation, rule.CorrectExample, rule.IncorrectExample, rule.Notes, rule.Topic}, " ")
		if topicMatches && strings.Contains(normalize(content), normalize(p.Query)) {
			p.Rules = append(p.Rules, rule)
		}
	}
	sort.Slice(p.Topics, func(i, j int) bool { return strings.ToLower(p.Topics[i]) < strings.ToLower(p.Topics[j]) })
	return p, rows.Err()
}
func cleanRule(rule Rule, topics []string) (Rule, error) {
	rule.Title = strings.Join(strings.Fields(rule.Title), " ")
	rule.Explanation = strings.TrimSpace(rule.Explanation)
	rule.CorrectExample = strings.TrimSpace(rule.CorrectExample)
	rule.IncorrectExample = strings.TrimSpace(rule.IncorrectExample)
	rule.Notes = strings.TrimSpace(rule.Notes)
	var err error
	rule.Topic, err = cleanGroup(rule.Topic, topics)
	if err != nil {
		return rule, fmt.Errorf("Keep topic names within 80 characters.")
	}
	if rule.Title == "" || rule.Explanation == "" {
		return rule, fmt.Errorf("Give your rule a title and an explanation.")
	}
	if len([]rune(rule.Title)) > 160 || len([]rune(rule.Explanation)) > 5000 || len([]rune(rule.CorrectExample)) > 2000 || len([]rune(rule.IncorrectExample)) > 2000 || len([]rune(rule.Notes)) > 3000 {
		return rule, fmt.Errorf("Keep the title within 160 characters, explanation within 5,000, examples within 2,000 each, and notes within 3,000.")
	}
	return rule, nil
}
func (a *App) renderRules(w http.ResponseWriter, r *http.Request, p RulesPage, editorOnly bool) {
	name := "rules-page"
	if r.Header.Get("HX-Request") == "true" {
		name = "rules-workspace"
		if editorOnly {
			name = "rule-editor"
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, p); err != nil {
		log.Print(err)
	}
}
func (a *App) serveRules(w http.ResponseWriter, r *http.Request) {
	p, err := a.rulesPage(r)
	if err != nil {
		log.Print(err)
		http.Error(w, "Rules notebook unavailable", 500)
		return
	}
	switch r.URL.Path {
	case "/rules":
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
	case "/rules/edit", "/rules/delete":
		isDelete := r.URL.Path == "/rules/delete"
		if (isDelete && r.Method != "POST") || (!isDelete && r.Method != "POST" && r.Method != "GET") {
			http.Error(w, "Method not allowed", 405)
			return
		}
		idText := r.FormValue("id")
		var original *Rule
		if idText != "" {
			id, e := strconv.ParseInt(idText, 10, 64)
			if e == nil {
				for _, rule := range p.AllRules {
					if rule.ID == id {
						copy := rule
						original = &copy
						break
					}
				}
			}
			if original == nil {
				http.Error(w, "This rule no longer exists.", 404)
				return
			}
		} else if isDelete {
			http.Error(w, "Choose a rule to remove.", 400)
			return
		}
		if isDelete {
			_, err = a.db.Exec("DELETE FROM rules WHERE id=?", original.ID)
			if err == nil {
				p, err = a.rulesPage(r)
				p.Feedback = "Rule removed."
			}
		} else if r.Method == "GET" {
			p.Editor = original
			if p.Editor == nil {
				p.Editor = &Rule{}
			}
			a.renderRules(w, r, p, true)
			return
		} else {
			submitted := Rule{Title: r.FormValue("title"), Explanation: r.FormValue("explanation"), CorrectExample: r.FormValue("correct_example"), IncorrectExample: r.FormValue("incorrect_example"), Notes: r.FormValue("notes"), Topic: r.FormValue("topic")}
			if original != nil {
				submitted.ID = original.ID
			}
			cleaned, validationErr := cleanRule(submitted, p.Topics)
			if validationErr != nil {
				p.Editor = &submitted
				p.EditorError = validationErr.Error()
			} else {
				if original == nil {
					_, err = a.db.Exec("INSERT INTO rules(title,explanation,correct_example,incorrect_example,notes,topic) VALUES(?,?,?,?,?,?)", cleaned.Title, cleaned.Explanation, cleaned.CorrectExample, cleaned.IncorrectExample, cleaned.Notes, cleaned.Topic)
				} else {
					_, err = a.db.Exec("UPDATE rules SET title=?,explanation=?,correct_example=?,incorrect_example=?,notes=?,topic=? WHERE id=?", cleaned.Title, cleaned.Explanation, cleaned.CorrectExample, cleaned.IncorrectExample, cleaned.Notes, cleaned.Topic, cleaned.ID)
				}
				if err == nil {
					p, err = a.rulesPage(r)
					p.Feedback = "Rule saved."
				}
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Print(err)
		http.Error(w, "Could not save your rule. Please try again.", 500)
		return
	}
	a.renderRules(w, r, p, false)
}
