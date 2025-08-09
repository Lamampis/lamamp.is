package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"flag"
	"html"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	_ "modernc.org/sqlite"
)

const guestbookPageSize = 7

var templates *template.Template
var db *sql.DB
var visitorCount int
var visitorCountMutex sync.Mutex

// --- Data Types ---
type PageData struct {
	Title   string
	Content template.HTML
	Theme   string
	ModTime time.Time
}

type GuestbookEntry struct {
	Name      string
	Message   string
	Timestamp time.Time
}

type GardenPage struct {
	Slug    string
	Title   string
	Created time.Time
	ModTime time.Time
	Tags    []string
}

type Anime struct {
	Rank     int
	Title    string
	ImageURL string
	Tier     string
	Comments string
}

type SearchResult struct {
	Title   string
	URL     string
	Snippet string
	Type    string
}

type MarkdownMeta struct {
	Slug    string
	Title   string
	Created time.Time
	ModTime time.Time
	HTML    template.HTML
}

// --- Theme ---
func getTheme(r *http.Request) string {
	theme := "light"
	if cookie, err := r.Cookie("theme"); err == nil {
		theme = cookie.Value
	}
	return theme
}

func setThemeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		theme := r.FormValue("theme")
		if theme != "light" && theme != "dark" {
			theme = "dark"
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "theme",
			Value:    theme,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400 * 30,
		})
	}
	http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)
}

// --- Markdown ---
func renderMarkdownFile(path string) (template.HTML, string, time.Time, time.Time, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}
	modTime := info.ModTime()
	created := time.Time{}

	title := ""
	body := content

	contentStr := string(content)
	if strings.HasPrefix(contentStr, "---") {
		parts := strings.SplitN(contentStr, "---", 3)
		if len(parts) >= 3 {
			meta := parts[1]
			body = []byte(parts[2])
			for _, line := range strings.Split(meta, "\n") {
				line = strings.TrimSpace(line)
				if after, found := strings.CutPrefix(line, "title:"); found {
					title = strings.TrimSpace(after)
				} else if after, found := strings.CutPrefix(line, "created:"); found {
					cs := strings.TrimSpace(after)
					formats := []string{
						time.RFC3339, "2006-01-02", "02-01-2006", "02/01/2006", "2 Jan 2006",
					}
					for _, layout := range formats {
						if t, err := time.Parse(layout, cs); err == nil {
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
		return "", "", time.Time{}, time.Time{}, err
	}
	return template.HTML(buf.String()), title, created, modTime, nil
}

func loadMarkdownFiles() ([]MarkdownMeta, error) {
	files, err := filepath.Glob("garden/*.md")
	if err != nil {
		return nil, err
	}
	var result []MarkdownMeta
	for _, f := range files {
		html, title, created, modTime, err := renderMarkdownFile(f)
		if err != nil {
			continue
		}
		slug := strings.TrimSuffix(filepath.Base(f), ".md")
		if title == "" {
			title = cases.Title(language.English).String(slug)
		}
		result = append(result, MarkdownMeta{
			Slug:    slug,
			Title:   title,
			Created: created,
			ModTime: modTime,
			HTML:    html,
		})
	}
	return result, nil
}

// --- DB ---
func connectDB() *sql.DB {
	db, err := sql.Open("sqlite", "./site.db")
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}
	return db
}

func getRandomQuote() string {
	row := db.QueryRow(`SELECT text FROM quotes ORDER BY RANDOM() LIMIT 1`)
	var quote string
	if err := row.Scan(&quote); err != nil {
		return "Error loading quote."
	}
	return quote
}

func loadGuestbookEntries(limit, offset int) ([]GuestbookEntry, error) {
	rows, err := db.Query(`SELECT name, message, timestamp FROM guestbook_entries ORDER BY timestamp DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []GuestbookEntry
	for rows.Next() {
		var e GuestbookEntry
		if err := rows.Scan(&e.Name, &e.Message, &e.Timestamp); err == nil {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func loadAnimeRankings() ([]Anime, error) {
	rows, err := db.Query(`SELECT rank, title, image_url, tier, comments FROM anime_rankings ORDER BY rank ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Anime
	for rows.Next() {
		var a Anime
		if err := rows.Scan(&a.Rank, &a.Title, &a.ImageURL, &a.Tier, &a.Comments); err != nil {
			continue
		}
		list = append(list, a)
	}
	return list, nil
}

func getGuestbookEntryCount() int {
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM guestbook_entries`).Scan(&count)
	return count
}

// --- HTML Helpers ---
func stripHTML(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, " ")
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func getSnippet(text, query string) string {
	text = stripHTML(text)
	if len(text) <= 200 {
		return text
	}
	pos := strings.Index(strings.ToLower(text), strings.ToLower(query))
	if pos == -1 {
		return text[:200] + "..."
	}
	start := max(0, pos-100)
	end := min(len(text), pos+100)
	snippet := text[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet += "..."
	}
	return snippet
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func renderSnippets(names []string, data map[string]any) (template.HTML, error) {
	var buf bytes.Buffer
	for _, name := range names {
		if err := templates.ExecuteTemplate(&buf, name, data[name]); err != nil {
			return "", err
		}
	}
	return template.HTML(buf.String()), nil
}

func renderPage(w http.ResponseWriter, data PageData) {
	_ = templates.ExecuteTemplate(w, "base", data)
}

func loadTemplates() *template.Template {
	funcs := template.FuncMap{
		"join": strings.Join,
		"dateFormat": func(t time.Time) string {
			return t.Format("02 Jan 2006")
		},
	}

	tmpl := template.Must(template.New("base").Funcs(funcs).Parse(`{{ define "base" }}{{ template "header" . }}{{ .Content }}{{ template "footer" . }}{{ end }}`))

	return template.Must(tmpl.ParseFiles(
		"templates/base/header.html",
		"templates/base/footer.html",
		"templates/base/404.html",
		"templates/home/welcome.html",
		"templates/home/quotes.html",
		"templates/home/introduction.html",
		"templates/home/latest_posts.html",
		"templates/home/guestbook.html",
		"templates/home/guest_comments.html",
		"templates/home/searchbar.html",
		"templates/garden/garden_main.html",
		"templates/garden/return.html",
		"templates/base/search_results.html",
		"templates/anime/anime_table.html",
		"templates/cyber/cyber.html",
		"templates/about/about.html",
	))
}

// --- Handlers ---
func formHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	name := html.EscapeString(r.FormValue("name"))
	if name == "" {
		name = "Anonymous"
	}
	message := html.EscapeString(r.FormValue("message"))
	_, err := db.Exec(`INSERT INTO guestbook_entries (name, message) VALUES (?, ?)`, name, message)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if n, _ := strconv.Atoi(p); n > 0 {
			page = n
		}
	}
	theme := getTheme(r)
	mdFiles, _ := loadMarkdownFiles()
	sort.Slice(mdFiles, func(i, j int) bool { return mdFiles[i].Created.After(mdFiles[j].Created) })

	var pages []GardenPage
	for i, md := range mdFiles {
		if i >= 10 {
			break
		}
		pages = append(pages, GardenPage{Slug: md.Slug, Title: md.Title, ModTime: md.Created})
	}
	entries, _ := loadGuestbookEntries(guestbookPageSize, (page-1)*guestbookPageSize)
	totalEntries := getGuestbookEntryCount()

	content, _ := renderSnippets([]string{
		"welcome.html", "quotes.html", "searchbar.html", "introduction.html", "latest_posts.html", "guestbook.html", "guest_comments.html",
	}, map[string]any{
		"guest_comments.html": map[string]any{"Entries": entries},
		"quotes.html":         map[string]any{"Quote": getRandomQuote()},
		"latest_posts.html":   map[string]any{"Pages": pages},
	})
	content += generatePagination(page, totalEntries, guestbookPageSize)

	renderPage(w, PageData{"Home", content, theme, time.Time{}})
}

func gardenHandler(w http.ResponseWriter, r *http.Request) {
	theme := getTheme(r)
	mdFiles, _ := loadMarkdownFiles()
	var pages []GardenPage
	for _, md := range mdFiles {
		pages = append(pages, GardenPage{Slug: md.Slug, Title: md.Title, Created: md.Created, ModTime: md.ModTime})
	}
	content, _ := renderSnippets([]string{"garden_main.html"}, map[string]any{"garden_main.html": map[string]any{"Pages": pages}})
	renderPage(w, PageData{"Garden", content, theme, time.Time{}})
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if query == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	var results []SearchResult
	mdFiles, _ := loadMarkdownFiles()
	for _, md := range mdFiles {
		if strings.Contains(strings.ToLower(md.Title), query) || strings.Contains(strings.ToLower(string(md.HTML)), query) {
			results = append(results, SearchResult{md.Title, "/garden/" + md.Slug, getSnippet(string(md.HTML), query), "garden"})
		}
	}
	if aboutContent, err := os.ReadFile("templates/about/about.html"); err == nil {
		if text := stripHTML(string(aboutContent)); strings.Contains(strings.ToLower(text), query) {
			results = append(results, SearchResult{"About", "/about", getSnippet(text, query), "page"})
		}
	}
	if cyberContent, err := os.ReadFile("templates/cyber/cyber.html"); err == nil {
		if text := stripHTML(string(cyberContent)); strings.Contains(strings.ToLower(text), query) {
			results = append(results, SearchResult{"Cyber", "/cyber", getSnippet(text, query), "page"})
		}
	}
	if animeList, err := loadAnimeRankings(); err == nil {
		for _, a := range animeList {
			if strings.Contains(strings.ToLower(a.Title), query) || strings.Contains(strings.ToLower(a.Comments), query) || strings.Contains(strings.ToLower(a.Tier), query) {
				results = append(results, SearchResult{a.Title + " (Anime)", "/anilist", a.Comments, "anime"})
			}
		}
	}
	content, _ := renderSnippets([]string{"search_results.html"}, map[string]any{"search_results.html": map[string]any{"Query": query, "Results": results, "Count": len(results)}})
	renderPage(w, PageData{"Search Results", content, getTheme(r), time.Time{}})
}

// --- Pagination ---
func generatePagination(currentPage, totalItems, pageSize int) template.HTML {
	totalPages := (totalItems + pageSize - 1) / pageSize
	if totalPages <= 1 {
		return ""
	}
	var buf bytes.Buffer
	buf.WriteString(`<div class="pagination">`)
	for i := 1; i <= totalPages; i++ {
		if i == currentPage {
			buf.WriteString(`>[<span class="current-page">` + strconv.Itoa(i) + `</span>]< `)
		} else {
			buf.WriteString(`<a href="/?page=` + strconv.Itoa(i) + `">[` + strconv.Itoa(i) + `]</a> `)
		}
	}
	buf.WriteString(`</div>`)
	return template.HTML(buf.String())
}

// --- Middleware ---
func generateUserID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func loggingMiddleware(next http.Handler) http.Handler {
	ignoredExtensions := map[string]struct{}{
		".jpg":   {},
		".jpeg":  {},
		".woff2": {},
		".png":   {},
		".gif":   {},
		".svg":   {},
		".ico":   {},
		".css":   {},
		".js":    {},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ext := filepath.Ext(r.URL.Path)
		if r.Method == "GET" {
			if _, ok := ignoredExtensions[ext]; ok {
				next.ServeHTTP(w, r)
				return
			}
		}

		var userID string
		isNewUser := false
		cookie, err := r.Cookie("user_id")
		if err != nil {
			isNewUser = true
			userID = generateUserID()
			http.SetCookie(w, &http.Cookie{
				Name:     "user_id",
				Value:    userID,
				Path:     "/",
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   86400 * 365 * 10,
			})
		} else {
			userID = cookie.Value
		}

		if isNewUser {
			visitorCountMutex.Lock()
			visitorCount++
			log.Printf("NEW VISITOR [%s] Total Visitors: %d", userID, visitorCount)
			visitorCountMutex.Unlock()
		} else {
			log.Printf("[%s] %s %s", userID, r.Method, r.URL.Path)
		}

		next.ServeHTTP(w, r)
	})
}

// --- Routes ---
func registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/")
		if path == "" || path == "home" {
			homeHandler(w, r)
			return
		}
		if slug, found := strings.CutPrefix(r.URL.Path, "/garden/"); found {
			if _, err := os.Stat(filepath.Join("garden", slug+".md")); err == nil {
				html, title, _, modTime, _ := renderMarkdownFile(filepath.Join("garden", slug+".md"))
				renderPage(w, PageData{title, html, getTheme(r), modTime})
				return
			}
		}
		switch r.URL.Path {
		case "/form":
			formHandler(w, r)
		case "/set-theme":
			setThemeHandler(w, r)
		case "/about":
			content, _ := renderSnippets([]string{"about.html"}, nil)
			renderPage(w, PageData{"About", content, getTheme(r), time.Time{}})
		case "/garden":
			gardenHandler(w, r)
		case "/anilist":
			animeList, _ := loadAnimeRankings()
			content, _ := renderSnippets([]string{"anime_table.html"}, map[string]any{"anime_table.html": animeList})
			renderPage(w, PageData{"My Anime Rankings", content, getTheme(r), time.Time{}})
		case "/cyber":
			content, _ := renderSnippets([]string{"cyber.html"}, nil)
			renderPage(w, PageData{"Cyber", content, getTheme(r), time.Time{}})
		case "/search":
			searchHandler(w, r)
		default:
			if strings.HasPrefix(r.URL.Path, "/static/") {
				http.StripPrefix("/static/", http.FileServer(http.Dir("static"))).ServeHTTP(w, r)
			} else {
				content, _ := renderSnippets([]string{"404.html"}, nil)
				renderPage(w, PageData{"404", content, getTheme(r), time.Time{}})
			}
		}
	})
}

// --- Server ---
func runLocalHTTP(handler http.Handler) {
	log.Println("Running in development mode on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", handler))
}

func runAutocert(handler http.Handler) {
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
	go func() {
		log.Println("Redirecting HTTP to HTTPS...")
		_ = http.ListenAndServe(":80", certManager.HTTPHandler(nil))
	}()
	log.Println("Server running on https://lamamp.is")
	log.Fatal(server.ListenAndServeTLS("", ""))
}

// --- Main ---
func main() {
	prod := flag.Bool("prod", false, "Run in production mode")
	flag.Parse()
	templates = loadTemplates()
	db = connectDB()
	defer db.Close()

	mux := http.NewServeMux()
	registerRoutes(mux)
	handler := loggingMiddleware(mux)

	if *prod {
		runAutocert(handler)
	} else {
		runLocalHTTP(handler)
	}
}
