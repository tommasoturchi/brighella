package main

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/miekg/dns"
	"golang.org/x/net/html"
)

const (
	what            = "brighella"
	dnsPrefix       = "_frame"
	dnsTitlePrefix  = "_frame_title"
	dnsFaviconPrefix = "_frame_favicon"
	resolverAddress = "8.8.8.8"
	resolverPort    = "53"
)

var (
	httpPort string
)

func init() {
	httpPort = os.Getenv("PORT")
	if httpPort == "" {
		httpPort = "5000"
	}
}

func main() {
	log.Printf("Starting %s...\n", what)

	server := NewServer()

	log.Printf("%s listening on %s...\n", what, httpPort)
	if err := http.ListenAndServe(":"+httpPort, server); err != nil {
		log.Panic(err)
	}
}

// Server represents a front-end web server.
type Server struct {
	// Router which handles incoming requests
	mux *http.ServeMux
}

// NewServer returns a new front-end web server that handles HTTP requests for the app.
func NewServer() *Server {
	router := http.NewServeMux()
	server := &Server{mux: router}
	router.HandleFunc("/", server.Root)
	return server
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Root is the handler for the HTTP requests to /.
// Any request that is not for the root path / is automatically redirected
// to the root with a 302 status code. Only a request to / will enable the iframe.
func (s *Server) Root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.TemporaryRedirect(w, r, "/")
	} else {
		targetURL, err := queryRedirectTarget(r.Host)
		// An error happened. For now, do not display the full error.
		if err != nil {
			http.Error(w, "Unable to find redirect target", http.StatusBadRequest)
			return
		}
		s.MaskedRedirect(w, r, targetURL)
	}
}

func (s *Server) TemporaryRedirect(w http.ResponseWriter, r *http.Request, strURL string) {
	http.Redirect(w, r, strURL, http.StatusTemporaryRedirect)
}

func (s *Server) MaskedRedirect(w http.ResponseWriter, r *http.Request, strURL string) {
	w.Header().Set("Content-type", "text/html")
	
	// Get metadata with all fallbacks
	title, favicon := getMetadata(r.Host, strURL)

	t, err := template.ParseFiles("redirect.tmpl")
	if err != nil {
		http.Error(w, "Template parsing error", http.StatusInternalServerError)
		return
	}
	t.Execute(w, &frame{
		Src:     strURL,
		Title:   title,
		Favicon: favicon,
	})
}

func queryRedirectTarget(host string) (string, error) {
	targetFqdn := fmt.Sprintf("%s.%s", dnsPrefix, host)

	c := new(dns.Client)
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(targetFqdn), dns.TypeTXT)
	r, _, err := c.Exchange(m, net.JoinHostPort(resolverAddress, resolverPort))

	if err != nil {
		log.Printf("[%s] Error querying %s: %v", host, targetFqdn, err)
		return "", err
	}

	if r.Rcode != dns.RcodeSuccess {
		err = fmt.Errorf("answer from %s not successful: %v", targetFqdn, dns.RcodeToString[r.Rcode])
		log.Printf("[%s] Error %s", host, err)
		return "", err
	}

	for _, a := range r.Answer {
		switch rr := a.(type) {
		case *dns.TXT:
			log.Printf("[%s] Found redirect target at %s: %v", host, targetFqdn, rr.Txt[0])
			return rr.Txt[0], nil
		}
	}

	err = fmt.Errorf("redirect target not found at %s", targetFqdn)
	log.Printf("[%s] Error %s", host, err)

	return "", err
}

func getTitle() string {
    title := os.Getenv("FRAME_TITLE")
    if title == "" {
        title = "Brighella"
    }
    return title
}

type frame struct {
	Src      string
	Title    string
	Favicon  string
}

// fetchPageMetadata retrieves the title and favicon from a URL
func fetchPageMetadata(url string) (title, favicon string, err error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch page: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read page: %v", err)
	}

	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", "", fmt.Errorf("failed to parse HTML: %v", err)
	}

	// Extract title and favicon
	var extractMetadata func(*html.Node)
	extractMetadata = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if n.FirstChild != nil {
					title = n.FirstChild.Data
				}
			case "link":
				// Look for favicon in link tags
				var rel, href string
				for _, attr := range n.Attr {
					switch attr.Key {
					case "rel":
						rel = attr.Val
					case "href":
						href = attr.Val
					}
				}
				if rel == "icon" || rel == "shortcut icon" {
					favicon = href
					// Convert relative URL to absolute
					if !strings.HasPrefix(favicon, "http") {
						if strings.HasPrefix(favicon, "/") {
							// Get base URL
							baseURL := url
							if idx := strings.Index(baseURL[8:], "/"); idx != -1 {
								baseURL = baseURL[:8+idx]
							}
							favicon = baseURL + favicon
						} else {
							favicon = url + "/" + favicon
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extractMetadata(c)
		}
	}
	extractMetadata(doc)

	// If no favicon found, use a default one
	if favicon == "" {
		favicon = "https://fav.farm/📸"
	}

	return title, favicon, nil
}

// queryDNSTXTRecord queries a specific TXT record and returns its value if found
func queryDNSTXTRecord(host, recordName string) (string, error) {
	targetFqdn := fmt.Sprintf("%s.%s", recordName, host)

	c := new(dns.Client)
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(targetFqdn), dns.TypeTXT)
	r, _, err := c.Exchange(m, net.JoinHostPort(resolverAddress, resolverPort))

	if err != nil {
		return "", fmt.Errorf("error querying %s: %v", targetFqdn, err)
	}

	if r.Rcode != dns.RcodeSuccess {
		return "", fmt.Errorf("answer from %s not successful: %v", targetFqdn, dns.RcodeToString[r.Rcode])
	}

	for _, a := range r.Answer {
		switch rr := a.(type) {
		case *dns.TXT:
			if len(rr.Txt) > 0 {
				return rr.Txt[0], nil
			}
		}
	}

	return "", fmt.Errorf("record not found: %s", targetFqdn)
}

// getMetadata tries to get metadata in this order:
// 1. Custom DNS TXT records (_frame_title and _frame_favicon)
// 2. Fetch from destination page
// 3. Fallback to defaults
func getMetadata(host, targetURL string) (title, favicon string) {
	// Try to get custom title from DNS
	customTitle, err := queryDNSTXTRecord(host, dnsTitlePrefix)
	if err == nil && customTitle != "" {
		title = customTitle
	} else {
		// Try to get custom favicon from DNS
		customFavicon, err := queryDNSTXTRecord(host, dnsFaviconPrefix)
		if err == nil && customFavicon != "" {
			favicon = customFavicon
		}

		// If we don't have both title and favicon from DNS, try to fetch from the page
		if title == "" || favicon == "" {
			pageTitle, pageFavicon, err := fetchPageMetadata(targetURL)
			if err == nil {
				if title == "" {
					title = pageTitle
				}
				if favicon == "" {
					favicon = pageFavicon
				}
			}
		}
	}

	// Fallback to defaults if still empty
	if title == "" {
		title = getTitle()
	}
	if favicon == "" {
		favicon = "https://fav.farm/🎭"
	}

	return title, favicon
}
