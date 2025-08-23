package main

import (
	"bytes"
	"database/sql"
	"flag"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	_ "modernc.org/sqlite"
)

const guestbookPageSize = 7

var (
	templates *template.Template
	db        *sql.DB
	contentDb *sql.DB
)

type PageData struct {
	Title   string
	Content template.HTML
	Theme   string
	ModTime time.Time
}

type GuestbookEntry struct {
	Name, Message string
	Timestamp     time.Time
}

type GardenPage struct {
	Slug, Title string
	Created     time.Time
	ModTime     time.Time
	Tags        []string
}

type Anime struct {
	Rank                            int
	Title, ImageURL, Tier, Comments string
}

type MarkdownMeta struct {
	Slug, Title      string
	Created, ModTime time.Time
	HTML             template.HTML
}

func getTheme(r *http.Request) string {
	if cookie, err := r.Cookie("theme"); err == nil {
		return cookie.Value
	}
	return "dark"
}

func initDB(path string, isComments bool) *sql.DB {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		log.Fatal(err)
	}

	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}

	var queries []string
	if isComments {
		queries = []string{
			`CREATE TABLE IF NOT EXISTS guestbook_entries (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				message TEXT NOT NULL,
				timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
			)`,
		}
	} else {
		queries = []string{
			`CREATE TABLE IF NOT EXISTS quotes (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				text TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS anime_rankings (
				rank INTEGER PRIMARY KEY,
				title TEXT NOT NULL,
				image_url TEXT,
				tier TEXT,
				comments TEXT
			)`,
		}
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			log.Fatal(err)
		}
	}

	return db
}

func parseMarkdown(path string) (template.HTML, string, time.Time, time.Time, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}

	info, _ := os.Stat(path)
	modTime := info.ModTime()
	created := modTime
	title := ""
	body := content

	if strings.HasPrefix(string(content), "---") {
		if parts := strings.SplitN(string(content), "---", 3); len(parts) >= 3 {
			body = []byte(parts[2])

			for _, line := range strings.Split(parts[1], "\n") {
				line = strings.TrimSpace(line)

				if after, found := strings.CutPrefix(line, "title:"); found {
					title = strings.TrimSpace(after)

				} else if after, found := strings.CutPrefix(line, "created:"); found {
					dateStr := strings.TrimSpace(after)

					// Support multiple date formats
					layouts := []string{
						"2006-01-02",   // YYYY-MM-DD
						"02-01-2006",   // DD-MM-YYYY
						"02 Jan 2006",  // e.g. 14 Jul 2025
						"Jan 02, 2006", // e.g. Jul 14, 2025
					}

					for _, layout := range layouts {
						if t, err := time.Parse(layout, dateStr); err == nil {
							created = t
							break
						}
					}
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := goldmark.Convert(body, &buf); err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}

	return template.HTML(buf.String()), title, created, modTime, nil
}

func getRandomQuote() string {
	var quote string
	contentDb.QueryRow(`SELECT text FROM quotes ORDER BY RANDOM() LIMIT 1`).Scan(&quote)
	return quote
}

func getGuestbookEntries(limit, offset int) []GuestbookEntry {
	rows, _ := db.Query(`SELECT name, message, timestamp FROM guestbook_entries ORDER BY timestamp DESC LIMIT ? OFFSET ?`, limit, offset)
	defer rows.Close()

	var entries []GuestbookEntry
	for rows.Next() {
		var e GuestbookEntry
		if rows.Scan(&e.Name, &e.Message, &e.Timestamp) == nil {
			entries = append(entries, e)
		}
	}

	return entries
}

func getAnimeRankings() []Anime {
	rows, _ := contentDb.Query(`SELECT rank, title, image_url, tier, comments FROM anime_rankings ORDER BY rank`)
	defer rows.Close()

	var list []Anime
	for rows.Next() {
		var a Anime
		if rows.Scan(&a.Rank, &a.Title, &a.ImageURL, &a.Tier, &a.Comments) == nil {
			list = append(list, a)
		}
	}

	return list
}

func getGuestbookCount() int {
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM guestbook_entries`).Scan(&count)
	return count
}

func loadTemplates() *template.Template {
	funcs := template.FuncMap{
		"join":       strings.Join,
		"dateFormat": func(t time.Time) string { return t.Format("02 Jan 2006") },
	}

	tmpl := template.Must(template.New("base").Funcs(funcs).Parse(
		`{{ define "base" }}{{ template "header" . }}{{ .Content }}{{ template "footer" . }}{{ end }}`))

	return template.Must(tmpl.ParseGlob("templates/*/*.html"))
}

func renderSnippets(names []string, data map[string]any) template.HTML {
	var buf bytes.Buffer

	for _, name := range names {
		templates.ExecuteTemplate(&buf, name, data[name])
	}

	return template.HTML(buf.String())
}

func renderPage(w http.ResponseWriter, data PageData) {
	templates.ExecuteTemplate(w, "base", data)
}

func pagination(page, total, size int) template.HTML {
	totalPages := (total + size - 1) / size
	if totalPages <= 1 {
		return ""
	}

	var buf bytes.Buffer
	buf.WriteString(`<div class="pagination">`)

	for i := 1; i <= totalPages; i++ {
		if i == page {
			buf.WriteString(fmt.Sprintf(`>[<span class="current-page">%d</span>]< `, i))
		} else {
			buf.WriteString(fmt.Sprintf(`<a href="/?page=%d">[%d]</a> `, i, i))
		}
	}

	buf.WriteString(`</div>`)
	return template.HTML(buf.String())
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	// for latest posts
	files, _ := filepath.Glob("garden/*.md")
	var pages []GardenPage

	for i, f := range files {
		if i >= 10 {
			break
		}

		if _, title, created, _, _ := parseMarkdown(f); title != "" {
			slug := strings.TrimSuffix(filepath.Base(f), ".md")
			pages = append(pages, GardenPage{Slug: slug, Title: title, ModTime: created})
		}
	}

	sort.Slice(pages, func(i, j int) bool { return pages[i].Created.After(pages[j].Created) })

	entries := getGuestbookEntries(guestbookPageSize, (page-1)*guestbookPageSize)

	content := renderSnippets([]string{
		"welcome.html", "quotes.html", "introduction.html", "latest_posts.html", "hotline.html", "guestbook.html", "guest_comments.html",
	}, map[string]any{
		"guest_comments.html": map[string]any{"Entries": entries},
		"quotes.html":         map[string]any{"Quote": getRandomQuote()},
		"latest_posts.html":   map[string]any{"Pages": pages},
	})

	content += pagination(page, getGuestbookCount(), guestbookPageSize)
	renderPage(w, PageData{"Home", content, getTheme(r), time.Time{}})
}

func gardenHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/garden")
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		files, _ := filepath.Glob("garden/*.md")
		var pages []GardenPage

		for _, f := range files {
			if _, title, created, modTime, _ := parseMarkdown(f); title != "" {
				slug := strings.TrimSuffix(filepath.Base(f), ".md")
				if title == "" {
					title = cases.Title(language.English).String(slug)
				}

				pages = append(pages, GardenPage{
					Slug: slug, Title: title, Created: created, ModTime: modTime,
				})
			}
		}

		content := renderSnippets([]string{"garden_main.html"},
			map[string]any{"garden_main.html": map[string]any{"Pages": pages}})
		renderPage(w, PageData{"Garden", content, getTheme(r), time.Time{}})
		return
	}

	if !strings.HasSuffix(path, ".md") {
		path += ".md"
	}
	fullPath := filepath.Join("garden", path)

	if _, err := os.Stat(fullPath); err != nil {
		// Use custom 404 page instead of http.NotFound
		w.WriteHeader(http.StatusNotFound)
		content := renderSnippets([]string{"404.html"}, nil)
		renderPage(w, PageData{"404", content, getTheme(r), time.Time{}})
		return
	}

	html, title, created, modTime, _ := parseMarkdown(fullPath)
	if title == "" {
		title = cases.Title(language.English).String(strings.TrimSuffix(path, ".md"))
	}

	dateInfo := template.HTML(fmt.Sprintf(
		`<div style="margin-bottom: 20px; color: #666; font-size: 0.9em;">Created: %s | Updated: %s</div>`,
		created.Format("02-01-2006"), modTime.Format("02-01-2006")))

	returnContent := renderSnippets([]string{"return.html"}, nil)
	content := dateInfo + html + template.HTML(returnContent)
	renderPage(w, PageData{title, content, getTheme(r), modTime})
}

func mainHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")

	switch {
	case path == "" || path == "home":
		homeHandler(w, r)

	case path == "garden" || strings.HasPrefix(r.URL.Path, "/garden/"):
		gardenHandler(w, r)

	case r.URL.Path == "/form" && r.Method == "POST":
		name := html.EscapeString(r.FormValue("name"))
		if name == "" {
			name = "Anonymous"
		}

		message := html.EscapeString(r.FormValue("message"))
		db.Exec(`INSERT INTO guestbook_entries (name, message) VALUES (?, ?)`, name, message)
		http.Redirect(w, r, "/", http.StatusSeeOther)

	case r.URL.Path == "/set-theme" && r.Method == "POST":
		theme := r.FormValue("theme")
		if theme != "light" && theme != "dark" {
			theme = "dark"
		}

		http.SetCookie(w, &http.Cookie{
			Name: "theme", Value: theme, Path: "/", HttpOnly: true,
			Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 86400 * 30,
		})
		http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)

	case r.URL.Path == "/about":
		content := renderSnippets([]string{"about.html"}, nil)
		renderPage(w, PageData{"About", content, getTheme(r), time.Time{}})

	case r.URL.Path == "/anilist":
		content := renderSnippets([]string{"anime_table.html"},
			map[string]any{"anime_table.html": getAnimeRankings()})
		renderPage(w, PageData{"My Anime Rankings", content, getTheme(r), time.Time{}})

	case r.URL.Path == "/cyber":
		content := renderSnippets([]string{"cyber.html"}, nil)
		renderPage(w, PageData{"Cyber", content, getTheme(r), time.Time{}})

	case r.URL.Path == "/riddles":
		content := renderSnippets([]string{"dark_coins.html", "return.html"}, nil)
		renderPage(w, PageData{"Riddles", content, getTheme(r), time.Time{}})

	case strings.HasPrefix(r.URL.Path, "/static/"):
		http.StripPrefix("/static/", http.FileServer(http.Dir("static"))).ServeHTTP(w, r)

	default:
		content := renderSnippets([]string{"404.html"}, nil)
		renderPage(w, PageData{"404", content, getTheme(r), time.Time{}})
	}
}

func main() {
	prod := flag.Bool("prod", false, "Run in production mode")
	flag.Parse()

	templates = loadTemplates()
	db = initDB("./comments.db", true)
	contentDb = initDB("./content.db", false)
	defer db.Close()
	defer contentDb.Close()

	// Wrap mainHandler so it satisfies http.Handler
	handler := http.HandlerFunc(mainHandler)

	if *prod {
		certManager := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist("lamamp.is", "www.lamamp.is"),
			Cache:      autocert.DirCache("/var/www/.cache"),
		}

		server := &http.Server{
			Addr:      ":443",
			Handler:   handler,
			TLSConfig: certManager.TLSConfig(),
		}

		// Redirect HTTP to HTTPS
		go http.ListenAndServe(":80", certManager.HTTPHandler(nil))

		log.Println("Server running on https://lamamp.is")
		log.Fatal(server.ListenAndServeTLS("", ""))

	} else {
		log.Println("Running in development mode on http://localhost:8080")
		log.Fatal(http.ListenAndServe(":8080", handler))
	}
}
