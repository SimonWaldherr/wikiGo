package main

import (
"flag"
"fmt"
"html/template"
"io"
"net"
"net/http"
"regexp"
"strings"
"time"

mmd "github.com/SimonWaldherr/micromarkdownGo"
sqlite "github.com/mxk/go-sqlite/sqlite3"
)

var (
templates = template.Must(template.ParseFiles(
"layout.html", "edit.html", "view.html", "user.html",
"search.html", "index.html", "history.html", "all.html",
))
validPath = regexp.MustCompile(`^/(edit|save|view|user|history)/([a-zA-Z0-9_.()-]+)$`)
db, _     = sqlite.Open("./wiki.sqlite3")
)

// PageInfo holds summary information about a single wiki page.
type PageInfo struct {
Name      string
TimeStr   string
Timestamp int64
}

// PageRevision holds data about one revision of a wiki page.
type PageRevision struct {
Id      int
TimeStr string
IP      string
}

// PageData is the data structure passed to all HTML templates.
type PageData struct {
Title        string
Body         string        // raw Markdown source (for <textarea>)
HTML         template.HTML // rendered HTML (for display)
Pages        []PageInfo    // recent pages list (sidebar / index)
History      []PageRevision
Query        string // active search term
ResultCount  int
LastModified string
}

// dbRow is used to scan a row from the wiki table.
type dbRow struct {
Id        int
Name      string
Content   string
Timestamp int64
IP        string
}


// formatTime converts a Unix timestamp to a human-readable German date string.
func formatTime(ts int64) string {
return time.Unix(ts, 0).Format("02.01.2006 15:04")
}

// getClientIP extracts the real client IP from a request.
func getClientIP(r *http.Request) string {
if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
}
host, _, err := net.SplitHostPort(r.RemoteAddr)
if err != nil {
return r.RemoteAddr
}
return host
}

// scanRows iterates a sqlite3 statement and calls fn for each row.
// fn should call q.Scan(...) and return the scanned row.
func iterRows(q *sqlite.Stmt, fn func(*sqlite.Stmt) bool) {
if q == nil {
return
}
for {
if !fn(q) {
break
}
if q.Next() == io.EOF {
break
}
}
}

// getPages returns pages from the wiki table, one entry per page name (latest timestamp),
// ordered by most recently modified.
func getPages(limit int) []PageInfo {
var pages []PageInfo
// Use MAX(timestamp) to guarantee we get the correct latest timestamp per page name.
sqlStr := "SELECT name, MAX(timestamp) AS ts FROM wiki GROUP BY name ORDER BY ts DESC"
var args []interface{}
if limit > 0 {
sqlStr += " LIMIT ?"
args = append(args, limit)
}
q, err := db.Query(sqlStr+";", args...)
if err != nil {
return pages
}
iterRows(q, func(s *sqlite.Stmt) bool {
var name string
var ts int64
if err := s.Scan(&name, &ts); err != nil {
return false
}
pages = append(pages, PageInfo{
Name:    name,
TimeStr: formatTime(ts),
Timestamp: ts,
})
return true
})
return pages
}

// loadSource fetches the latest raw Markdown content for a page.
// Returns ("", nil) when the page does not exist yet.
func loadSource(title string) (string, int64, error) {
q, err := db.Query("SELECT id, name, content, timestamp, ip FROM wiki WHERE name = ? ORDER BY timestamp DESC LIMIT 1;", title)
if err != nil {
return "", 0, err
}
var row dbRow
if err := q.Scan(&row.Id, &row.Name, &row.Content, &row.Timestamp, &row.IP); err != nil {
return "", 0, nil // page not found
}
return row.Content, row.Timestamp, nil
}

// loadHistory returns all revisions for a given page, newest first.
func loadHistory(title string) []PageRevision {
var history []PageRevision
q, err := db.Query("SELECT id, name, content, timestamp, ip FROM wiki WHERE name = ? ORDER BY timestamp DESC;", title)
if err != nil {
return history
}
iterRows(q, func(s *sqlite.Stmt) bool {
var row dbRow
if err := s.Scan(&row.Id, &row.Name, &row.Content, &row.Timestamp, &row.IP); err != nil {
return false
}
history = append(history, PageRevision{
Id:      row.Id,
TimeStr: formatTime(row.Timestamp),
IP:      row.IP,
})
return true
})
return history
}

// saveSource inserts a new revision of a page into the database.
func saveSource(title, body, ip string) error {
ts := time.Now().Unix()
return db.Exec("INSERT INTO wiki (name, content, timestamp, ip) VALUES(?, ?, ?, ?);",
title, body, ts, ip)
}

// renderTemplate executes the named template and writes the result to w.
func renderTemplate(w http.ResponseWriter, tmpl string, data *PageData) {
w.Header().Set("Content-Type", "text/html; charset=utf-8")
if err := templates.ExecuteTemplate(w, tmpl+".html", data); err != nil {
http.Error(w, err.Error(), http.StatusInternalServerError)
}
}

// logRequest prints a simple access log line.
func logRequest(r *http.Request) {
fmt.Printf("%s  %-6s  %s  %s\n",
time.Now().Format("2006-01-02 15:04:05"),
r.Method,
getClientIP(r),
r.URL.String(),
)
}

// ----- Handlers -----

func rootHandler(w http.ResponseWriter, r *http.Request) {
logRequest(r)
data := &PageData{
Title: "Startseite",
Pages: getPages(20),
}
renderTemplate(w, "index", data)
}

func allPagesHandler(w http.ResponseWriter, r *http.Request) {
logRequest(r)
data := &PageData{
Title: "Alle Seiten",
Pages: getPages(0), // no limit – show everything
}
renderTemplate(w, "all", data)
}

func viewHandler(w http.ResponseWriter, r *http.Request, title string) {
raw, ts, err := loadSource(title)
if err != nil || raw == "" {
http.Redirect(w, r, "/edit/"+title, http.StatusFound)
return
}
data := &PageData{
Title:        title,
HTML:         template.HTML(mmd.Micromarkdown(raw)), //nolint:gosec
Pages:        getPages(15),
LastModified: formatTime(ts),
}
renderTemplate(w, "view", data)
}

func editHandler(w http.ResponseWriter, r *http.Request, title string) {
raw, _, _ := loadSource(title)
data := &PageData{
Title: title,
Body:  raw,
Pages: getPages(15),
}
renderTemplate(w, "edit", data)
}

func saveHandler(w http.ResponseWriter, r *http.Request, title string) {
if r.Method != http.MethodPost {
http.Redirect(w, r, "/edit/"+title, http.StatusSeeOther)
return
}
if err := r.ParseForm(); err != nil {
http.Error(w, "Ungültige Formulardaten", http.StatusBadRequest)
return
}
body := strings.ReplaceAll(r.PostFormValue("body"), "\r\n", "\n")
if err := saveSource(title, body, getClientIP(r)); err != nil {
http.Error(w, "Fehler beim Speichern: "+err.Error(), http.StatusInternalServerError)
return
}
http.Redirect(w, r, "/view/"+title, http.StatusFound)
}

func historyHandler(w http.ResponseWriter, r *http.Request, title string) {
data := &PageData{
Title:   title,
History: loadHistory(title),
Pages:   getPages(15),
}
renderTemplate(w, "history", data)
}

func userHandler(w http.ResponseWriter, r *http.Request, title string) {
renderTemplate(w, "user", &PageData{Title: title})
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
logRequest(r)
query := strings.TrimSpace(r.URL.Query().Get("q"))
if query == "" {
http.Redirect(w, r, "/", http.StatusFound)
return
}

// Use a subquery to get the most recent revision per page, then filter.
likeArg := "%" + query + "%"
sqlQuery := "SELECT w.name, w.content, w.timestamp FROM wiki w " +
"INNER JOIN (SELECT name, MAX(timestamp) AS ts FROM wiki GROUP BY name) latest " +
"ON w.name = latest.name AND w.timestamp = latest.ts " +
"WHERE w.name LIKE ? OR w.content LIKE ? ORDER BY w.timestamp DESC;"
q, err := db.Query(sqlQuery, likeArg, likeArg)
var buf strings.Builder
count := 0
if err == nil {
iterRows(q, func(s *sqlite.Stmt) bool {
var row dbRow
if err := s.Scan(&row.Name, &row.Content, &row.Timestamp); err != nil {
return false
}
count++
// Build excerpt: find query in content and show surrounding text
excerpt := buildExcerpt(row.Content, query, 180)
buf.WriteString(`<div class="result-item">`)
buf.WriteString(`<div class="result-title"><a href="/view/` + template.HTMLEscapeString(row.Name) + `">` + template.HTMLEscapeString(row.Name) + `</a></div>`)
if excerpt != "" {
buf.WriteString(`<div class="result-excerpt">` + excerpt + `</div>`)
}
buf.WriteString(`<div class="result-meta">Zuletzt geändert: ` + formatTime(row.Timestamp) + `</div>`)
buf.WriteString(`</div>`)
return true
})
}

data := &PageData{
Title:       "Suche: " + query,
Query:       query,
HTML:        template.HTML(buf.String()), //nolint:gosec
Pages:       getPages(15),
ResultCount: count,
}
renderTemplate(w, "search", data)
}

// buildExcerpt returns a short snippet of text around the first occurrence of query.
// Special HTML characters are escaped; the query match is highlighted with <mark>.
// Uses rune-based slicing to avoid splitting multi-byte UTF-8 characters.
func buildExcerpt(content, query string, maxLen int) string {
runes := []rune(content)
lowerRunes := []rune(strings.ToLower(content))
queryRunes := []rune(strings.ToLower(query))
// Find first occurrence of query in the rune slice.
idx := -1
for i := 0; i <= len(lowerRunes)-len(queryRunes); i++ {
match := true
for j, qr := range queryRunes {
if lowerRunes[i+j] != qr {
match = false
break
}
}
if match {
idx = i
break
}
}
if idx == -1 {
// No match in content; return beginning.
if len(runes) > maxLen {
return template.HTMLEscapeString(string(runes[:maxLen])) + "…"
}
return template.HTMLEscapeString(content)
}
const ctxBefore = 60
const ctxAfter = 120
start := idx - ctxBefore
if start < 0 {
start = 0
}
end := idx + len(queryRunes) + ctxAfter
if end > len(runes) {
end = len(runes)
}
prefix := ""
if start > 0 {
prefix = "…"
}
suffix := ""
if end < len(runes) {
suffix = "…"
}
before := template.HTMLEscapeString(string(runes[start:idx]))
matchStr := template.HTMLEscapeString(string(runes[idx : idx+len(queryRunes)]))
after := template.HTMLEscapeString(string(runes[idx+len(queryRunes) : end]))
return prefix + before + "<mark>" + matchStr + "</mark>" + after + suffix
}

// previewHandler renders Markdown to HTML for the live editor preview.
func previewHandler(w http.ResponseWriter, r *http.Request) {
if r.Method != http.MethodPost {
http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
return
}
if err := r.ParseForm(); err != nil {
http.Error(w, "Bad request", http.StatusBadRequest)
return
}
w.Header().Set("Content-Type", "text/html; charset=utf-8")
fmt.Fprint(w, mmd.Micromarkdown(r.PostFormValue("content")))
}

// makeHandler wraps a handler that needs a validated page title from the URL path.
func makeHandler(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
return func(w http.ResponseWriter, r *http.Request) {
logRequest(r)
m := validPath.FindStringSubmatch(r.URL.Path)
if m == nil {
http.NotFound(w, r)
return
}
fn(w, r, m[2])
}
}

func main() {
flag.Parse()
port := flag.Arg(0)
if port == "" {
port = "8080"
}
addr := ":" + port

// Ensure the wiki table exists.
if err := db.Exec("CREATE TABLE IF NOT EXISTS wiki (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, content TEXT, timestamp INTEGER, ip TEXT);"); err != nil {
fmt.Printf("Warnung: Tabellen-Erstellung fehlgeschlagen: %v\n", err)
}

server := &http.Server{
Addr:           addr,
ReadTimeout:    30 * time.Second,
WriteTimeout:   30 * time.Second,
MaxHeaderBytes: 1 << 20,
}

http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))
http.HandleFunc("/view/", makeHandler(viewHandler))
http.HandleFunc("/edit/", makeHandler(editHandler))
http.HandleFunc("/save/", makeHandler(saveHandler))
http.HandleFunc("/user/", makeHandler(userHandler))
http.HandleFunc("/history/", makeHandler(historyHandler))
http.HandleFunc("/search", searchHandler)
http.HandleFunc("/preview", previewHandler)
http.HandleFunc("/all", allPagesHandler)
http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
if r.URL.Path == "/" {
rootHandler(w, r)
return
}
http.NotFound(w, r)
})

fmt.Printf("wikiGo läuft auf http://localhost%s\n", addr)
if err := server.ListenAndServe(); err != nil {
fmt.Printf("Server-Fehler: %v\n", err)
}
}
