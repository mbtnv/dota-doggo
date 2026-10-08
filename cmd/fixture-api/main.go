// fixture-api is an isolated fake Telegram/OpenDota server for container checks.
// Its control endpoints are unauthenticated; never expose it outside a test network.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type fixtureAPI struct {
	mu       sync.Mutex
	recent   map[string]any
	phase    string
	updates  []map[string]any
	messages []map[string]any
	requests map[string]int
	sequence int
}

func main() {
	addr := flag.String("addr", ":8080", "listen address on an isolated test network")
	path := flag.String("recent", "/fixtures/opendota-recent.json", "anonymous recentMatches fixture")
	requestPath := flag.String("request", "", "send a local control request instead of starting the server")
	body := flag.String("data", "", "JSON body for the local control request")
	startTime := flag.Int64("start-time", 0, "fixed fixture Unix time; default is one hour ago")
	flag.Parse()
	if *requestPath != "" {
		method := http.MethodGet
		if *body != "" {
			method = http.MethodPost
		}
		req, err := http.NewRequest(method, "http://127.0.0.1:8080"+*requestPath, strings.NewReader(*body))
		if err != nil {
			log.Fatal(err)
		}
		if *body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		client := &http.Client{Timeout: 3 * time.Second}
		r, err := client.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			log.Fatal("control request failed: ", r.StatusCode)
		}
		_, _ = io.Copy(os.Stdout, r.Body)
		return
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		log.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil || len(rows) != 1 {
		log.Fatal("expected one recentMatches fixture")
	}
	// Keep the same fixed instant for every request and both implementations.
	if *startTime == 0 {
		*startTime = time.Now().UTC().Add(-time.Hour).Unix()
	}
	rows[0]["start_time"] = *startTime
	api := &fixtureAPI{recent: rows[0], phase: "initial", requests: map[string]int{}}
	server := &http.Server{Addr: *addr, Handler: api, ReadHeaderTimeout: 5 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func (f *fixtureAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	input := map[string]any{}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
	} else {
		_ = r.ParseForm()
		for key := range r.Form {
			input[key] = r.Form.Get(key)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	respond := func(value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	success := func(value any) { respond(map[string]any{"ok": true, "result": value}) }
	path := r.URL.Path
	if path == "/health" {
		respond(map[string]bool{"ok": true})
		return
	}
	if path == "/observations" {
		respond(map[string]any{"requests": f.requests, "messages": f.messages, "phase": f.phase, "recent": f.recent})
		return
	}
	if path == "/control" && r.Method == http.MethodPost {
		phase, _ := input["phase"].(string)
		switch phase {
		case "initial", "new", "poll_error", "block_recent":
			f.phase = phase
		default:
			http.Error(w, "unknown phase", 400)
			return
		}
		respond(map[string]bool{"ok": true})
		return
	}
	if path == "/updates" && r.Method == http.MethodPost {
		text, _ := input["text"].(string)
		f.sequence++
		f.updates = append(f.updates, map[string]any{
			"update_id": f.sequence, "message": map[string]any{
				"message_id": f.sequence, "date": time.Now().Unix(), "text": text,
				"from": map[string]any{"id": 123, "is_bot": false, "first_name": "Fixture"},
				"chat": map[string]any{"id": -100123, "type": "supergroup", "title": "Fixture"},
			},
		})
		respond(map[string]bool{"ok": true})
		return
	}
	f.requests[r.Method+" "+path]++
	if strings.HasPrefix(path, "/bot") {
		method := path[strings.LastIndex(path, "/")+1:]
		switch method {
		case "getMe":
			success(map[string]any{"id": 123456, "is_bot": true, "first_name": "Fixture", "username": "fixture_bot"})
		case "getUpdates":
			if f.phase == "poll_error" {
				w.WriteHeader(500)
				respond(map[string]any{"ok": false, "error_code": 500, "description": "fixture failure"})
				return
			}
			offset, _ := strconv.Atoi(fmt.Sprint(input["offset"]))
			updates := []map[string]any{}
			for _, update := range f.updates {
				if update["update_id"].(int) >= offset {
					updates = append(updates, update)
				}
			}
			if len(updates) == 0 {
				f.mu.Unlock()
				select {
				case <-r.Context().Done():
				case <-time.After(time.Second):
				}
				f.mu.Lock()
			}
			success(updates)
		case "sendMessage":
			f.messages = append(f.messages, input)
			success(map[string]any{"message_id": len(f.messages), "date": time.Now().Unix(), "chat": map[string]any{"id": -100123, "type": "supergroup"}, "text": input["text"]})
		case "getChatAdministrators":
			success([]any{map[string]any{"status": "creator", "is_anonymous": false, "user": map[string]any{"id": 123, "is_bot": false, "first_name": "Fixture"}}})
		case "deleteWebhook":
			success(true)
		default:
			http.Error(w, "unknown Telegram method", 404)
		}
		return
	}
	if strings.HasPrefix(path, "/api/constants/") {
		switch strings.TrimPrefix(path, "/api/constants/") {
		case "heroes":
			respond(map[string]any{"74": map[string]any{"id": 74, "localized_name": "Invoker"}})
		case "game_mode":
			respond(map[string]any{"22": map[string]any{"id": 22, "name": "All Pick"}})
		case "lobby_type":
			respond(map[string]any{"7": map[string]any{"id": 7, "name": "Ranked"}})
		default:
			http.Error(w, "unknown constants resource", 404)
		}
		return
	}
	if path == "/api/players/123" {
		respond(map[string]any{"profile": map[string]any{"account_id": 123, "personaname": "Fixture", "profileurl": "https://www.dotabuff.com/players/123"}})
		return
	}
	if path == "/api/players/123/recentMatches" || path == "/api/players/123/matches" {
		if f.phase == "block_recent" && strings.HasSuffix(path, "/recentMatches") {
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
			return
		}
		if offset, _ := strconv.Atoi(r.URL.Query().Get("offset")); offset > 0 {
			respond([]any{})
			return
		}
		rows := []any{f.recent}
		if f.phase == "new" || f.phase == "block_recent" {
			rows = append([]any{f.newMatch()}, rows...)
		}
		respond(rows)
		return
	}
	if path == "/api/matches/999" || path == "/api/matches/1000" {
		match := f.newMatch()
		if strings.HasSuffix(path, "/999") {
			match["match_id"] = 999
		}
		match["account_id"] = 123
		match["gold_per_min"] = 500
		match["xp_per_min"] = 600
		result := map[string]any{}
		for key, value := range match {
			result[key] = value
		}
		result["players"] = []any{match}
		respond(result)
		return
	}
	http.Error(w, "unknown fixture endpoint", 404)
}

func (f *fixtureAPI) newMatch() map[string]any {
	match := map[string]any{}
	for key, value := range f.recent {
		match[key] = value
	}
	match["match_id"] = 1000
	return match
}
