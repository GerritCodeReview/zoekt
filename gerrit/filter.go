package gerrit

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/zoekt/web"
)

type User struct {
	Name   string
	Server string
	UID    int
}

type contextKey int

var userContextKey contextKey

// NewContext returns a new Context that carries value u.
func newContext(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// FromContext returns the User value stored in ctx, if any.
func fromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userContextKey).(*User)
	return u, ok
}

type loginFilter struct {
	handler http.Handler

	mux       *http.ServeMux
	gerritURL string
	myURL     *url.URL

	mu        sync.Mutex
	cookieMap map[string]*User
}

func NewGerritLoginFilter(h http.Handler, gerritURL, myURL string) (http.Handler, error) {
	my, err := url.Parse(myURL)
	if err != nil {
		return nil, err
	}

	filter := &loginFilter{
		handler:   h,
		gerritURL: gerritURL,
		myURL:     my,
		cookieMap: map[string]*User{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/login", filter.login)
	mux.HandleFunc("/logout", filter.logout)
	mux.HandleFunc("/", filter.incoming)
	return mux, nil
}

const cookieName = "gerritID"

func (s *loginFilter) requestID(rw http.ResponseWriter, req *http.Request) {
	vals := make(url.Values)
	req.URL.Host = s.myURL.Host
	req.URL.Scheme = s.myURL.Scheme

	loginURL := *req.URL
	loginURL.Path = "/login"
	loginURL.RawQuery = ""
	vals["login"] = []string{loginURL.String()}
	vals["cont"] = []string{req.URL.String()}

	dest := s.gerritURL
	if !strings.HasSuffix(dest, "/") {
		dest += "/"
	}
	dest += "a/config/server/assertid?" + vals.Encode()

	http.Redirect(rw, req, dest, http.StatusFound)
}

func (s *loginFilter) incoming(rw http.ResponseWriter, req *http.Request) {
	ck, err := req.Cookie(cookieName)
	if err != nil {
		s.requestID(rw, req)
		return
	}

	s.mu.Lock()
	u := s.cookieMap[ck.Value]
	s.mu.Unlock()

	if u == nil {
		s.requestID(rw, req)
		return
	}

	req = req.WithContext(
		web.ContextWithUser(
			newContext(req.Context(), u), u.Name))
	s.handler.ServeHTTP(rw, req)
}

func (s *loginFilter) logout(rw http.ResponseWriter, req *http.Request) {
	ck := &http.Cookie{
		Name:   cookieName,
		MaxAge: -1,
	}
	http.SetCookie(rw, ck)
	rw.Write([]byte("logged out"))
}

func (s *loginFilter) login(rw http.ResponseWriter, req *http.Request) {
	qvals := req.URL.Query()
	if alg := qvals.Get("alg"); alg != "hmac-todo" {
		http.Error(rw, fmt.Sprintf("invalid mac algorithm %q", alg), http.StatusUnauthorized)
		return
	}
	if qvals.Get("sig") != "signature-todo" {
		http.Error(rw, "invalid signature algorithm", http.StatusUnauthorized)
		return
	}

	if ts, err := strconv.Atoi(qvals.Get("ts")); err != nil {
		http.Error(rw, "bad ts", http.StatusBadRequest)
		return
	} else {
		ts64 := int64(ts)
		now := time.Now().Unix()

		if ts64 > now+5 {
			http.Error(rw, "ts in future", http.StatusBadRequest)
			return
		}
		if ts64 < now-5 {
			http.Error(rw, "ts in past", http.StatusBadRequest)
			return
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var cookie [8]byte
	if _, err := rand.Read(cookie[:]); err != nil {
		http.Error(rw, "internal server error", http.StatusInternalServerError)
		return
	}

	id, err := strconv.Atoi(qvals.Get("uid"))
	if err != nil {
		http.Error(rw, "bad uid", http.StatusBadRequest)
		return
	}
	cont, err := url.Parse(qvals.Get("cont"))
	if err != nil {
		http.Error(rw, "bad cont URL", http.StatusBadRequest)
		return
	}

	u := &User{
		Name:   qvals.Get("nm"),
		Server: qvals.Get("w"),
		UID:    id,
	}

	cookieAscii := fmt.Sprintf("%x", cookie[:])
	s.cookieMap[cookieAscii] = u

	ck := &http.Cookie{
		Name:   cookieName, // TODO(hanwen): allow multiple gerrit IDs?
		Value:  cookieAscii,
		MaxAge: 24 * 3600,
	}

	http.SetCookie(rw, ck)
	http.Redirect(rw, req, cont.String(), http.StatusFound)
}
