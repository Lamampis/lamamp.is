package main

import (
	"bytes"
	"database/sql"
	"flag"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/feeds"
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
	Slug, Title, Image string
	Created            time.Time
	ModTime            time.Time
	Tags               []string
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

func parseMarkdown(path string) (template.HTML, string, string, time.Time, time.Time, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", time.Time{}, time.Time{}, err
	}

	info, _ := os.Stat(path)
	modTime := info.ModTime()
	created := time.Time{}
	title := ""
	image := ""
	body := content

	// Standardize line endings to \n (fixes Windows CRLF issues)
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")

	if strings.HasPrefix(normalized, "---") {
		if parts := strings.SplitN(normalized, "---", 3); len(parts) >= 3 {
			body = []byte(parts[2])

			for _, line := range strings.Split(parts[1], "\n") {
				line = strings.TrimSpace(line)

				if after, found := strings.CutPrefix(line, "title:"); found {
					title = strings.TrimSpace(after)
					title = strings.Trim(title, "\"'") // Clean surrounding quotes
				} else if after, found := strings.CutPrefix(line, "image:"); found {
					image = strings.TrimSpace(after)
					image = strings.Trim(image, "\"'") // Clean surrounding quotes
				} else if after, found := strings.CutPrefix(line, "cover:"); found {
					image = strings.TrimSpace(after)
					image = strings.Trim(image, "\"'") // Clean surrounding quotes
				} else if after, found := strings.CutPrefix(line, "created:"); found {
					dateStr := strings.TrimSpace(after)
					dateStr = strings.Trim(dateStr, "\"'")

					layouts := []string{
						"2006-01-02",
						"02-01-2006",
						"10-05-2006",
						"02 Jan 2006",
						"January 2 2006",
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

	if created.IsZero() {
		created = modTime
	}

	var buf bytes.Buffer
	if err := goldmark.Convert(body, &buf); err != nil {
		return "", "", "", time.Time{}, time.Time{}, err
	}

	return template.HTML(buf.String()), title, image, created, modTime, nil
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
		"dateFormat": func(t time.Time) string { return t.Format("January 2 2006") },
		"randomChoice": func(a, b string) string {
			if rand.Intn(2) == 0 {
				return a
			}
			return b
		},
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

	// collect latest posts
	files, _ := filepath.Glob("garden/*.md")
	var pages []GardenPage

	for _, f := range files {
		// Change this line: added an extra "_" for the image parameter
		if _, title, _, created, modTime, _ := parseMarkdown(f); title != "" {
			slug := strings.TrimSuffix(filepath.Base(f), ".md")
			pages = append(pages, GardenPage{
				Slug:    slug,
				Title:   title,
				Created: created,
				ModTime: modTime,
			})
		}
	}

	// sort newest first
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Created.After(pages[j].Created)
	})

	// only keep the 10 most recent
	if len(pages) > 10 {
		pages = pages[:10]
	}

	entries := getGuestbookEntries(guestbookPageSize, (page-1)*guestbookPageSize)

	content := renderSnippets([]string{
		"welcome.html",
		"quotes.html",
		"introduction.html",
		"latest_posts.html",
		// "blinkies.html", will uncomment when ready
		"hotline.html",
		"guestbook.html",
		"guest_comments.html",
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
			_, title, image, created, modTime, err := parseMarkdown(f)
			if err != nil {
				continue
			}

			slug := strings.TrimSuffix(filepath.Base(f), ".md")

			// Fallback: If no frontmatter title, use filename
			if title == "" {
				title = cases.Title(language.English).String(strings.ReplaceAll(slug, "-", " "))
			}

			// Fallback: Random image if not defined in markdown frontmatter
			if image == "" {
				image = "https://picsum.photos/400/300"
			}

			pages = append(pages, GardenPage{
				Slug:    slug,
				Title:   title,
				Image:   image,
				Created: created,
				ModTime: modTime,
			})
		}

		// Sort newest first
		sort.Slice(pages, func(i, j int) bool {
			return pages[i].Created.After(pages[j].Created)
		})

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
		w.WriteHeader(http.StatusNotFound)
		content := renderSnippets([]string{"404.html"}, nil)
		renderPage(w, PageData{"404", content, getTheme(r), time.Time{}})
		return
	}

	// Inside gardenHandler (single post rendering):

	html, title, _, created, modTime, _ := parseMarkdown(fullPath)
	if title == "" {
		title = cases.Title(language.English).String(strings.TrimSuffix(path, ".md"))
	}

	dateInfo := template.HTML(fmt.Sprintf(
		`<div style="margin-bottom: 20px; color: #666; font-size: 0.9em;">Created: %s | Updated: %s</div>`,
		created.Format("02-01-2006"), modTime.Format("02-01-2006")))

	returnContent := renderSnippets([]string{"return.html"}, nil)

	content := dateInfo + template.HTML(`<article class="post-content">`) + html + template.HTML(`</article>`) + template.HTML(returnContent)

	renderPage(w, PageData{title, content, getTheme(r), modTime})
}

func rssHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

	feed := &feeds.Feed{
		Title:       "Lamampis",
		Link:        &feeds.Link{Href: "https://lamamp.is/garden"},
		Description: "lamamp.is blog posts",
		Author:      &feeds.Author{Name: "Lamampis"},
		Created:     now,
	}

	files, _ := filepath.Glob("garden/*.md")
	var items []*feeds.Item

	for _, f := range files {
		// Parse markdown body and metadata
		htmlContent, title, _, created, _, err := parseMarkdown(f)
		if err != nil {
			continue
		}

		slug := strings.TrimSuffix(filepath.Base(f), ".md")

		if title == "" {
			title = cases.Title(language.English).String(strings.ReplaceAll(slug, "-", " "))
		}

		items = append(items, &feeds.Item{
			Title:       title,
			Link:        &feeds.Link{Href: "https://lamamp.is/garden/" + slug},
			Description: string(htmlContent),
			Created:     created,
		})
	}

	// Sort feed items newest first
	sort.Slice(items, func(i, j int) bool {
		return items[i].Created.After(items[j].Created)
	})

	feed.Items = items

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	if err := feed.WriteRss(w); err != nil {
		http.Error(w, "Failed to generate RSS feed", http.StatusInternalServerError)
	}
}

func routeHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")

	switch {
	case path == "" || path == "home":
		homeHandler(w, r)

	case path == "garden" || strings.HasPrefix(r.URL.Path, "/garden/"):
		gardenHandler(w, r)

	case r.URL.Path == "/form" && r.Method == "POST":
		if strings.TrimSpace(r.FormValue("email")) != "" {
			log.Printf("Spam bot blocked from guestbook submission")
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = "Anonymous"
		}

		message := strings.TrimSpace(r.FormValue("message"))

		if message == "" {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}

		if _, err := db.Exec(`INSERT INTO guestbook_entries (name, message) VALUES (?, ?)`, name, message); err != nil {
			log.Printf("failed to insert guestbook entry: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

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
		content := renderSnippets([]string{"anime_table.html", "returnhome.html"},
			map[string]any{"anime_table.html": getAnimeRankings()})
		renderPage(w, PageData{"My Anime Rankings", content, getTheme(r), time.Time{}})

	case r.URL.Path == "/cyber":
		content := renderSnippets([]string{"cyber.html", "returnhome.html"}, nil)
		renderPage(w, PageData{"Cyber", content, getTheme(r), time.Time{}})

	case strings.HasPrefix(r.URL.Path, "/static/"):
		http.StripPrefix("/static/", http.FileServer(http.Dir("static"))).ServeHTTP(w, r)

	case r.URL.Path == "/rss.xml":
		rssHandler(w, r)

	case path == "garden" || strings.HasPrefix(r.URL.Path, "/garden/"):
		gardenHandler(w, r)

	default:
		content := renderSnippets([]string{"404.html"}, nil)
		renderPage(w, PageData{"404", content, getTheme(r), time.Time{}})
	}
}

func main() {
	https := flag.Bool("https", false, "Run in production mode")
	flag.Parse()

	templates = loadTemplates()
	db = initDB("./comments.db", true)
	contentDb = initDB("./content.db", false)
	defer db.Close()
	defer contentDb.Close()

	handler := http.HandlerFunc(routeHandler)

	if *https {
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
