package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JMBugarin/classmap-transit/internal/transit"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, e := transit.Open(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal(e)
	}
	defer s.DB.Close()
	if os.Getenv("SKIP_MIGRATE") != "1" {
		if e = s.Migrate(ctx); e != nil {
			log.Fatal(e)
		}
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "schedule":
			b, e := os.ReadFile(os.Args[2])
			if e != nil {
				log.Fatal(e)
			}
			id, e := s.ImportSchedule(ctx, b)
			if e != nil {
				log.Fatal(e)
			}
			fmt.Println(id)
			return
		case "ingest":
			b, e := os.ReadFile(os.Args[2])
			if e != nil {
				log.Fatal(e)
			}
			id, e := s.Accept(ctx, "live", "trips", os.Getenv("SCHEDULE_VERSION"), b, time.Now())
			if e != nil {
				log.Fatal(e)
			}
			fmt.Println(id)
			return
		case "bench-seed":
			n, e := strconv.Atoi(envOr("BENCH_ROWS", "1000000"))
			if e != nil || n < 1 || n > 10_000_000 {
				log.Fatal("BENCH_ROWS must be 1..10000000")
			}
			seed, e := strconv.ParseUint(envOr("BENCH_SEED", "20261115"), 10, 64)
			if e != nil {
				log.Fatal(e)
			}
			id, e := s.SeedBench(ctx, n, seed, time.Now())
			if e != nil {
				log.Fatal(e)
			}
			fmt.Println(id)
			return
		case "prune":
			keep, e := time.ParseDuration(envOr("RETAIN", "168h"))
			if e != nil || keep < time.Hour {
				log.Fatal("RETAIN must be at least 1h")
			}
			n, e := s.Prune(ctx, time.Now().Add(-keep), 1000)
			if e != nil {
				log.Fatal(e)
			}
			fmt.Println(n)
			return
		case "activity-exclude":
			if len(os.Args) < 3 {
				log.Fatal("usage: activity-exclude <install id or participant code>...")
			}
			for _, id := range os.Args[2:] {
				if e = s.ExcludeActivity(ctx, id); e != nil {
					log.Fatal(e)
				}
			}
			return
		case "activity-report":
			var cohort []string
			if len(os.Args) > 2 {
				f, e := os.Open(os.Args[2])
				if e != nil {
					log.Fatal(e)
				}
				cohort, e = transit.ReadCohort(f)
				f.Close()
				if e != nil {
					log.Fatal(e)
				}
				if cohort == nil {
					cohort = []string{}
				}
			}
			weeks, e := s.ActivityReport(ctx, cohort)
			if e != nil {
				log.Fatal(e)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(weeks)
			return
		case "replay":
			if len(os.Args) < 4 || os.Args[2] == "live" {
				log.Fatal("replay requires an isolated namespace and capture files")
			}
			ns := "replay-" + os.Args[2]
			for _, path := range os.Args[3:] {
				b, e := os.ReadFile(path)
				if e != nil {
					log.Fatal(e)
				}
				m, e := transit.Decode(b)
				if e != nil {
					log.Fatal(e)
				}
				at := time.Unix(int64(m.Header.GetTimestamp()), 0)
				s.Now = func() time.Time { return at }
				if _, e = s.Accept(ctx, ns, "trips", "", b, at); e != nil {
					log.Fatal(e)
				}
				j, e := s.ClaimNamespace(ctx, ns)
				if e == nil {
					if e = s.Process(ctx, j); e != nil {
						log.Fatal(e)
					}
				}
			}
			fp, e := s.Fingerprint(ctx, ns)
			if e != nil {
				log.Fatal(e)
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"namespace": ns, "fingerprint": fp})
			return
		}
	}
	workers, e := strconv.Atoi(envOr("WORKERS", "2"))
	if e != nil || workers < 1 || workers > 16 {
		log.Fatal("WORKERS must be 1..16")
	}
	var wg sync.WaitGroup
	if feed := os.Getenv("POLL_URL"); feed != "" {
		every, e := time.ParseDuration(envOr("POLL_INTERVAL", "30s"))
		if e != nil || every < 30*time.Second {
			log.Fatal("POLL_INTERVAL must be at least 30s")
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			transit.Poll(ctx, s, http.DefaultClient, feed, os.Getenv("SCHEDULE_VERSION"), every, log.Printf)
		}()
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.Worker(ctx) }()
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	srv := &http.Server{Addr: addr, Handler: &transit.API{Store: s, Cache: os.Getenv("RESPONSE_CACHE") == "1", Token: os.Getenv("ADMIN_TOKEN"), Origin: os.Getenv("ALLOWED_ORIGIN")}, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	log.Printf("listening %s", addr)
	if e = srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Print(e)
		stop()
	}
	stop()
	wg.Wait()
}

func envOr(k, v string) string {
	if x := os.Getenv(k); x != "" {
		return x
	}
	return v
}
