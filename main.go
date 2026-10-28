package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	loadDotEnv(".env")
	importPath := flag.String("import", "", "import vans, run log and settings from the Google Sheet downloaded as .xlsx, then exit")
	force := flag.Bool("force", false, "with -import: replace data even if the database already has vans")
	defaultAddr := ":8080"
	if port := os.Getenv("PORT"); port != "" { // set by most hosting platforms
		defaultAddr = ":" + port
	}
	addr := flag.String("addr", envOr("ADDR", defaultAddr), "listen address")
	flag.Parse()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set: copy .env.example to .env and paste the Supabase connection string")
	}
	store, err := OpenStore(dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	if *importPath != "" {
		if err := runImport(store, *importPath, *force); err != nil {
			log.Fatal(err)
		}
		return
	}

	supabaseURL, publishableKey := os.Getenv("SUPABASE_URL"), os.Getenv("SUPABASE_PUBLISHABLE_KEY")
	if supabaseURL == "" || publishableKey == "" {
		log.Fatal("SUPABASE_URL and SUPABASE_PUBLISHABLE_KEY must be set for login (see .env.example)")
	}
	srv, err := newServer(store, NewAuth(supabaseURL, publishableKey))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on http://localhost%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv))
}

func runImport(store *Store, path string, force bool) error {
	existing, err := store.Vans()
	if err != nil {
		return err
	}
	if len(existing) > 0 && !force {
		return fmt.Errorf("the database already has %d vans; add -force to replace everything with %s", len(existing), path)
	}
	st, vans, logs, err := readWorkbook(path)
	if err != nil {
		return err
	}
	if err := store.ReplaceAll(st, vans, logs); err != nil {
		return err
	}
	log.Printf("imported %d vans, %d run log rows, date %s, %d vans/day", len(vans), len(logs), st.Date, st.Needed)
	return nil
}

// loadDotEnv sets KEY=VALUE pairs from a .env file without overriding the real environment.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
