package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"flag"

	"github.com/yuin/goldmark"
	"golang.org/x/crypto/acme/autocert"
	_ "modernc.org/sqlite"
)

const guestbookPageSize = 7

var templates *template.Template
var db *sql.DB

// Data to load for Each Page
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
	Type    string // "garden", "page", "anime", "guestbook"
}

// Theme Stuff
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
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400 * 30,
		})
	}
	referer := r.Header.Get("Referer")
	if referer == "" {
		referer = "/"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

// Markdown
func markdownHandler(w http.ResponseWriter, r *http.Request) {
	theme := getTheme(r)
	slug := strings.TrimPrefix(r.URL.Path, "/garden/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}

	mdPath := filepath.Join("garden", slug+".md")
	html, title, created, modTime, err := renderMarkdownFile(mdPath)
	if err != nil {
		http.Error(w, "Markdown not found", http.StatusNotFound)
		return
	}
	if title == "" {
		title = strings.Title(slug)
	}

	var contentBuf bytes.Buffer
	contentBuf.WriteString(fmt.Sprintf(
		`<p><em>Created: %s | Last updated: %s</em></p>`,
		created.Format("02-01-2006"),
		modTime.Format("02-01-2006"),
	))
	contentBuf.WriteString(string(html))

	returnLink, err := renderSnippets([]string{"return.html"}, nil)
	if err != nil {
		log.Printf("Error rendering returnlink snippet: %v", err)
		returnLink = ""
	}
	contentBuf.WriteString(string(returnLink))

	renderPage(w, PageData{
		Title:   title,
		Content: template.HTML(contentBuf.String()),
		Theme:   theme,
	})
}
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
				if strings.HasPrefix(line, "title:") {
					title = strings.TrimSpace(strings.TrimPrefix(line, "title:"))
				} else if strings.HasPrefix(line, "created:") {
					cs := strings.TrimSpace(strings.TrimPrefix(line, "created:"))
					formats := []string{
						time.RFC3339, // 2025-07-22T10:00:00Z
						"2006-01-02", // 2025-07-22
						"02-01-2006", // 22-07-2025
						"02/01/2006", // 22/07/2025
						"2 Jan 2006", // 22 Jul 2025
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

// Fetching from DB
func connectDB() *sql.DB {
	db, err := sql.Open("sqlite", "./site.db")
	if err != nil {
		log.Fatal("SQLite open error:", err)
	}
	if err := db.Ping(); err != nil {
		log.Fatal("SQLite ping error:", err)
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

// Tools
// Helper function to strip HTML tags
func stripHTML(s string) string {
	// Remove HTML tags
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, " ")

	// Clean up multiple spaces and newlines
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")

	return strings.TrimSpace(s)
}

// Helper function to get a snippet around the search term
func getSnippet(text, query string) string {
	// Strip HTML tags first
	text = stripHTML(text)
	text = strings.TrimSpace(text)

	if len(text) <= 200 {
		return text
	}

	lowerText := strings.ToLower(text)
	pos := strings.Index(lowerText, strings.ToLower(query))
	if pos == -1 {
		return text[:200] + "..."
	}

	start := pos - 100
	if start < 0 {
		start = 0
	}

	end := pos + 100
	if end > len(text) {
		end = len(text)
	}

	snippet := text[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet = snippet + "..."
	}

	return snippet
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if query == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var results []SearchResult

	// 1. Search Garden posts
	files, _ := filepath.Glob("garden/*.md")
	for _, f := range files {
		slug := strings.TrimSuffix(filepath.Base(f), ".md")
		body, title, _, _, err := renderMarkdownFile(f)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(title), query) ||
			strings.Contains(strings.ToLower(string(body)), query) {
			results = append(results, SearchResult{
				Title:   title,
				URL:     "/garden/" + slug,
				Snippet: getSnippet(string(body), query),
				Type:    "garden",
			})
		}
	}

	// 2. Search About page
	aboutContent, err := os.ReadFile("templates/about/about.html")
	if err == nil {
		aboutText := stripHTML(string(aboutContent))
		if strings.Contains(strings.ToLower(aboutText), query) {
			results = append(results, SearchResult{
				Title:   "About",
				URL:     "/about",
				Snippet: getSnippet(aboutText, query),
				Type:    "page",
			})
		}
	}
	// 3. Search Cyber page
	cyberContent, err := os.ReadFile("templates/cyber/cyber.html")
	if err == nil {
		aboutText := stripHTML(string(cyberContent))
		if strings.Contains(strings.ToLower(aboutText), query) {
			results = append(results, SearchResult{
				Title:   "Cyber",
				URL:     "/cyber",
				Snippet: getSnippet(aboutText, query),
				Type:    "page",
			})
		}
	}

	// 4. Search Anime rankings
	animeList, err := loadAnimeRankings()
	if err == nil {
		for _, a := range animeList {
			if strings.Contains(strings.ToLower(a.Title), query) ||
				strings.Contains(strings.ToLower(a.Comments), query) ||
				strings.Contains(strings.ToLower(a.Tier), query) {
				results = append(results, SearchResult{
					Title:   a.Title + " (Anime)",
					URL:     "/anilist",
					Snippet: a.Comments,
					Type:    "anime",
				})
			}
		}
	}

	content, err := renderSnippets([]string{"search_results.html"}, map[string]any{
		"search_results.html": map[string]any{
			"Query":   query,
			"Results": results,
			"Count":   len(results),
		},
	})
	if err != nil {
		http.Error(w, "Render error", http.StatusInternalServerError)
		return
	}

	renderPage(w, PageData{
		Title:   "Search Results",
		Content: content,
		Theme:   getTheme(r),
	})
}

func renderSnippets(snippetNames []string, dataMap map[string]any) (template.HTML, error) {
	var buf bytes.Buffer
	for _, name := range snippetNames {
		data := dataMap[name]
		if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
			log.Printf("template error for snippet %q: %v", name, err)
			return "", err
		}
	}
	return template.HTML(buf.String()), nil
}
func renderPage(w http.ResponseWriter, data PageData) {
	if err := templates.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("template execution error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
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

func generatePagination(currentPage, totalItems, pageSize int) template.HTML {
	totalPages := (totalItems + pageSize - 1) / pageSize
	if totalPages <= 1 {
		return ""
	}

	var paginationBuf bytes.Buffer
	paginationBuf.WriteString(`<div class="pagination">`)
	for i := 1; i <= totalPages; i++ {
		if i == currentPage {
			paginationBuf.WriteString(`>[<span class="current-page">` + strconv.Itoa(i) + `</span>]< `)
		} else {
			paginationBuf.WriteString(`<a href="/?page=` + strconv.Itoa(i) + `">[` + strconv.Itoa(i) + `]</a> `)
		}
	}
	paginationBuf.WriteString(`</div>`)

	return template.HTML(paginationBuf.String())
}
func formHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		name = "Anonymous"
	}
	message := r.FormValue("message")

	_, err := db.Exec(`INSERT INTO guestbook_entries (name, message) VALUES (?, ?)`, name, message)
	if err != nil {
		log.Println("DB insert error:", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func getGuestbookEntryCount() int {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM guestbook_entries`).Scan(&count)
	if err != nil {
		log.Println("Count error:", err)
		return 0
	}
	return count
}

// Pages
func homeHandler(w http.ResponseWriter, r *http.Request) {
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			page = n
		}
	}

	theme := getTheme(r)
	files, err := filepath.Glob("garden/*.md")
	if err != nil {
		http.Error(w, "Failed to read garden content", http.StatusInternalServerError)
		return
	}

	type mdWithTime struct {
		Path    string
		Created time.Time
	}

	var mdFiles []mdWithTime
	for _, f := range files {
		_, _, created, _, err := renderMarkdownFile(f)
		if err != nil {
			continue
		}
		if created.IsZero() {
			info, err := os.Stat(f)
			if err != nil {
				continue
			}
			created = info.ModTime()
		}
		mdFiles = append(mdFiles, mdWithTime{Path: f, Created: created})
	}

	sort.Slice(mdFiles, func(i, j int) bool {
		return mdFiles[i].Created.After(mdFiles[j].Created)
	})

	var pages []GardenPage
	for _, md := range mdFiles {
		slug := strings.TrimSuffix(filepath.Base(md.Path), ".md")
		_, title, _, _, err := renderMarkdownFile(md.Path)
		if err != nil {
			continue
		}
		if title == "" {
			title = strings.Title(slug)
		}

		pages = append(pages, GardenPage{
			Slug:    slug,
			Title:   title,
			ModTime: md.Created,
		})
	}

	if len(pages) > 10 {
		pages = pages[:10]
	}

	entries, err := loadGuestbookEntries(guestbookPageSize, (page-1)*guestbookPageSize)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	totalEntries := getGuestbookEntryCount()

	snippets := []string{"welcome.html", "quotes.html", "searchbar.html", "introduction.html", "latest_posts.html", "guestbook.html", "guest_comments.html"}
	snippetData := map[string]any{
		"guest_comments.html": map[string]any{"Entries": entries},
		"quotes.html":         map[string]any{"Quote": getRandomQuote()},
		"latest_posts.html":   map[string]any{"Pages": pages},
	}
	content, err := renderSnippets(snippets, snippetData)
	if err != nil {
		http.Error(w, "Render error", http.StatusInternalServerError)
		return
	}
	content += generatePagination(page, totalEntries, guestbookPageSize)

	renderPage(w, PageData{
		Title:   "Home",
		Content: content,
		Theme:   theme,
	})
}
func gardenHandler(w http.ResponseWriter, r *http.Request) {
	theme := getTheme(r)

	files, err := filepath.Glob("garden/*.md")
	if err != nil {
		http.Error(w, "Failed to read garden content", http.StatusInternalServerError)
		return
	}

	var pages []GardenPage
	for _, f := range files {
		slug := strings.TrimSuffix(filepath.Base(f), ".md")

		// Get file info (modification time)
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		modTime := info.ModTime()

		// Extract title, created time, and mod time from the markdown file
		_, title, created, _, err := renderMarkdownFile(f)
		if err != nil || title == "" {
			title = strings.Title(slug)
		}

		pages = append(pages, GardenPage{
			Slug:    slug,
			Title:   title,
			Created: created,
			ModTime: modTime,
		})
	}

	// Pass all pages with both Created and ModTime to the template
	snippetData := map[string]any{
		"garden_main.html": map[string]any{"Pages": pages},
	}
	content, err := renderSnippets([]string{"garden_main.html"}, snippetData)
	if err != nil {
		http.Error(w, "Render error", http.StatusInternalServerError)
		return
	}

	renderPage(w, PageData{
		Title:   "Garden",
		Content: content,
		Theme:   theme,
	})
}
func animeListHandler(w http.ResponseWriter, r *http.Request) {
	theme := getTheme(r)

	animeList, err := loadAnimeRankings()
	if err != nil {
		http.Error(w, "Error loading anime rankings", http.StatusInternalServerError)
		return
	}

	content, err := renderSnippets([]string{"anime_table.html"}, map[string]any{
		"anime_table.html": animeList,
	})
	if err != nil {
		http.Error(w, "Render error", http.StatusInternalServerError)
		return
	}

	renderPage(w, PageData{
		Title:   "My Anime Rankings",
		Content: content,
		Theme:   theme,
	})
}
func cyberHandler(w http.ResponseWriter, r *http.Request) {
	theme := getTheme(r)
	content, err := renderSnippets([]string{"cyber.html"}, nil)
	if err != nil {
		http.Error(w, "Render error", http.StatusInternalServerError)
		return
	}
	renderPage(w, PageData{
		Title:   "Cyber",
		Content: content,
		Theme:   theme,
	})
}

func aboutHandler(w http.ResponseWriter, r *http.Request) {
	theme := getTheme(r)
	content, err := renderSnippets([]string{"about.html"}, nil)
	if err != nil {
		http.Error(w, "Render error", http.StatusInternalServerError)
		return
	}
	renderPage(w, PageData{
		Title:   "About",
		Content: content,
		Theme:   theme,
	})
}

// 404 Page
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	theme := getTheme(r)
	content, err := renderSnippets([]string{"404.html"}, nil)
	if err != nil {
		http.Error(w, "Render error", http.StatusInternalServerError)
		return
	}
	renderPage(w, PageData{
		Title:   "404",
		Content: content,
		Theme:   theme,
	})
}

// Main

func main() {
	prod := flag.Bool("prod", false, "Run in production mode (HTTPS with autocert)")
	flag.Parse()

	templates = loadTemplates()
	db = connectDB()
	defer db.Close()

	mux := http.NewServeMux()
	registerRoutes(mux)

	if *prod {
		runAutocert(mux)
	} else {
		runLocalHTTP(mux)
	}
}

func mode(prod bool) string {
	if prod {
		return "PRODUCTION (autocert HTTPS)"
	}
	return "DEVELOPMENT (HTTP only)"
}

func registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/")
		if path == "" || path == "home" {
			homeHandler(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/garden/") {
			slug := strings.TrimPrefix(r.URL.Path, "/garden/")
			if slug != "" {
				mdPath := filepath.Join("garden", slug+".md")
				if _, err := os.Stat(mdPath); err == nil {
					markdownHandler(w, r)
					return
				}
			}
		}

		switch r.URL.Path {
		case "/form":
			formHandler(w, r)
		case "/set-theme":
			setThemeHandler(w, r)
		case "/about":
			aboutHandler(w, r)
		case "/garden":
			gardenHandler(w, r)
		case "/anilist":
			animeListHandler(w, r)
		case "/cyber":
			cyberHandler(w, r)
		case "/search":
			searchHandler(w, r)
		default:
			if strings.HasPrefix(r.URL.Path, "/static/") {
				http.StripPrefix("/static/", http.FileServer(http.Dir("static"))).ServeHTTP(w, r)
			} else {
				notFoundHandler(w, r)
			}
		}
	})
}

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

	// HTTP to HTTPS redirection
	go func() {
		log.Println("Redirecting HTTP to HTTPS...")
		if err := http.ListenAndServe(":80", certManager.HTTPHandler(nil)); err != nil {
			log.Fatal(err)
		}
	}()

	log.Println("Server running on https://lamamp.is")
	log.Fatal(server.ListenAndServeTLS("", ""))
}
