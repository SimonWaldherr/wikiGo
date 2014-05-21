package main

import (
	"flag"
	"fmt"
	mmd "github.com/SimonWaldherr/micromarkdownGo"
	"github.com/mxk/go-sqlite/sqlite3"
	"io"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
)

var (
	templates = template.Must(template.ParseFiles("edit.html", "view.html", "user.html", "search.html", "index.html"))
	validPath = regexp.MustCompile("^/(edit|save|view|user|search)/([a-zA-Z0-9_()]+)$")
	sql, _    = sqlite3.Open("./wiki.sqlite3")
)

type Page struct {
	Title string
	Body  string
}

type Qu struct {
	Id        int
	Name      string
	Content   string
	Timestamp int
	IP        string
}

func (p *Page) saveSource(r *http.Request) error {
	return sql.Exec("INSERT INTO wiki (name, content, timestamp, ip) VALUES('" + p.Title + "', '" + strings.Replace(p.Body, "'", "''", -1) + "', '" + strconv.FormatInt(time.Now().Unix(), 10) + "', '" + strings.Split(r.RemoteAddr, ":")[0] + "');")
}

func (p *Page) saveCache() error {
	filename := "cache/" + p.Title + ".html"
	return ioutil.WriteFile(filename, []byte(p.Body), 0600)
}

func loadSource(title string) (*Page, error) {
	var qu Qu
	q, err := sql.Query("SELECT * FROM wiki WHERE name = '" + title + "' ORDER BY timestamp DESC LIMIT 1;")
	if err != nil {
		return &Page{Title: title, Body: ""}, nil
	} else {
		err = q.Scan(&qu.Id, &qu.Name, &qu.Content, &qu.Timestamp)
		if err != nil {
			fmt.Printf("Error while getting row data: %s\n", err)
		}
		return &Page{Title: qu.Name, Body: qu.Content}, nil
	}
}

func loadCache(title string) (*Page, error) {
	filename := "cache/" + title + ".html"
	body, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return &Page{Title: title, Body: string(body)}, nil
}

func renderTemplate(w http.ResponseWriter, tmpl string, p *Page) {
	err := templates.ExecuteTemplate(w, tmpl+".html", p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func viewHandler(w http.ResponseWriter, r *http.Request, title string) {
	p, err := loadCache(title)
	if err != nil {
		http.Redirect(w, r, "/edit/"+title, http.StatusFound)
		return
	}
	renderTemplate(w, "view", p)
}

func userHandler(w http.ResponseWriter, r *http.Request, title string) {
	p := &Page{Title: "", Body: ""}
	renderTemplate(w, "user", p)
}

func searchHandler(w http.ResponseWriter, r *http.Request, title string) {
	var qu Qu
	q, _ := sql.Query("SELECT id, name, content, timestamp FROM wiki WHERE name like '%" + title + "%' GROUP BY name ORDER BY timestamp DESC;")
	retstr := ""
	ioeof := false
	for ioeof != true {
		q.Scan(&qu.Id, &qu.Name, &qu.Content, &qu.Timestamp)
		retstr += "<li><a href=\"/view/" + qu.Name + "\">" + qu.Name + "</a></li>"
		if q.Next() == io.EOF {
			ioeof = true
		}
	}
	retstr = "<ul>" + retstr + "</ul>"
	renderTemplate(w, "search", &Page{Title: title, Body: retstr})
}

func editHandler(w http.ResponseWriter, r *http.Request, title string) {
	p, _ := loadSource(title)
	renderTemplate(w, "edit", p)
}

func saveHandler(w http.ResponseWriter, r *http.Request, title string) {
	body := r.FormValue("body")
	p := &Page{Title: title, Body: body}
	err := p.saveSource(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		fmt.Printf("1 Error while getting row data: %s\n", err)
		return
	}
	p = &Page{Title: title, Body: mmd.Micromarkdown(body)}
	err = p.saveCache()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		fmt.Printf("2 Error while getting row data: %s\n", err)
		return
	}
	http.Redirect(w, r, "/view/"+title, http.StatusFound)
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	var qu Qu
	q, _ := sql.Query("SELECT id, name, content, timestamp FROM wiki GROUP BY name ORDER BY timestamp DESC;")
	retstr := ""
	ioeof := false
	for ioeof != true {
		q.Scan(&qu.Id, &qu.Name, &qu.Content, &qu.Timestamp)
		retstr += "<li><a href=\"./view/" + qu.Name + "\">" + qu.Name + "</a></li>"
		if q.Next() == io.EOF {
			ioeof = true
		}
	}
	retstr = "<ul>" + retstr + "</ul>"
	renderTemplate(w, "index", &Page{Title: qu.Name, Body: retstr})
}

func makeHandler(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := validPath.FindStringSubmatch(r.URL.Path)
		fmt.Println(strconv.FormatInt(time.Now().Unix(), 10) + ": " + strings.Split(r.RemoteAddr, ":")[0] + " - " + r.URL.Path)
		if m == nil && r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		} else if r.URL.Path == "/" {
			var qu Qu
			q, _ := sql.Query("SELECT id, name, content, timestamp FROM wiki GROUP BY name ORDER BY timestamp DESC;")
			q.Scan(&qu.Id, &qu.Name, &qu.Content, &qu.Timestamp)
			retstr := ""
			for q.Next() != io.EOF {
				retstr += "<li><a href=\"./view/" + qu.Name + "\">" + qu.Name + "</a></li>"
				q.Scan(&qu.Id, &qu.Name, &qu.Content, &qu.Timestamp)
			}
			retstr += "<li><a href=\"./view/" + qu.Name + "\">" + qu.Name + "</a></li>"
			retstr = "<ul>" + retstr + "</ul>"
			renderTemplate(w, "index", &Page{Title: qu.Name, Body: retstr})
			return
		}
		fn(w, r, m[2])
	}
}

func main() {
	flag.Parse()
	port := ":" + flag.Arg(0)
	if port == ":" {
		port = ":8080"
	}
	server := &http.Server{
		Addr:           port,
		ReadTimeout:    20 * time.Second,
		WriteTimeout:   20 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))
	http.HandleFunc("/view/", makeHandler(viewHandler))
	http.HandleFunc("/edit/", makeHandler(editHandler))
	http.HandleFunc("/save/", makeHandler(saveHandler))
	http.HandleFunc("/user/", makeHandler(userHandler))
	http.HandleFunc("/search/", makeHandler(searchHandler))
	http.HandleFunc("/", makeHandler(viewHandler))
	server.ListenAndServe()
}
