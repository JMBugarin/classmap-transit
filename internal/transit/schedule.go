package transit

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"strconv"
	"strings"
	"time"
)

func ServiceTime(date time.Time, seconds int, zone *time.Location) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, 12, 0, 0, 0, zone).Add(time.Duration(seconds-43200) * time.Second)
}
func ParseTime(s string) (int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, errors.New("bad time")
	}
	n := []int{}
	for _, p := range parts {
		x, e := strconv.Atoi(p)
		if e != nil || x < 0 {
			return 0, errors.New("bad time")
		}
		n = append(n, x)
	}
	if n[0] >= 168 || n[1] > 59 || n[2] > 59 {
		return 0, errors.New("bad time")
	}
	return n[0]*3600 + n[1]*60 + n[2], nil
}
func (s *Store) ImportSchedule(ctx context.Context, raw []byte) (string, error) {
	if len(raw) > 32<<20 {
		return "", errors.New("zip too large")
	}
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return "", e
	}
	tables := map[string][]map[string]string{}
	for _, f := range z.File {
		if !strings.HasSuffix(f.Name, ".txt") {
			continue
		}
		r, e := f.Open()
		if e != nil {
			return "", e
		}
		b, e := io.ReadAll(io.LimitReader(r, 64<<20+1))
		r.Close()
		if e != nil || len(b) > 64<<20 {
			return "", errors.New("oversize table")
		}
		rows, e := csv.NewReader(bytes.NewReader(b)).ReadAll()
		if e != nil {
			return "", e
		}
		if len(rows) == 0 {
			continue
		}
		for _, row := range rows[1:] {
			m := map[string]string{}
			for i, k := range rows[0] {
				m[strings.TrimPrefix(k, "\ufeff")] = row[i]
			}
			tables[f.Name] = append(tables[f.Name], m)
		}
	}
	if len(tables["agency.txt"]) == 0 {
		return "", errors.New("missing agency")
	}
	zone := tables["agency.txt"][0]["agency_timezone"]
	if _, e = time.LoadLocation(zone); e != nil {
		return "", e
	}
	id := Hash(raw)
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SET LOCAL statement_timeout = 0"); e != nil {
		return "", e
	}
	tag, e := tx.Exec(ctx, "INSERT INTO schedules(id,timezone,raw) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", id, zone, raw)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() == 0 {
		return id, tx.Commit(ctx)
	}
	copyRows := func(table string, cols []string, rows [][]any) error {
		_, err := tx.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		return nil
	}
	date := func(v string) (time.Time, error) { return time.Parse("20060102", v) }
	rows := [][]any{}
	for _, r := range tables["routes.txt"] {
		rows = append(rows, []any{id, r["route_id"], strings.TrimSpace(r["route_short_name"] + " " + r["route_long_name"])})
	}
	if e = copyRows("routes", []string{"version", "id", "name"}, rows); e != nil {
		return "", e
	}
	rows = rows[:0]
	for _, r := range tables["stops.txt"] {
		rows = append(rows, []any{id, r["stop_id"], r["stop_name"]})
	}
	if e = copyRows("stops", []string{"version", "id", "name"}, rows); e != nil {
		return "", e
	}
	calendars, services := [][]any{}, map[string]bool{}
	for _, r := range tables["calendar.txt"] {
		days := []bool{}
		for _, d := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
			days = append(days, r[d] == "1")
		}
		from, err := date(r["start_date"])
		if err != nil {
			return "", err
		}
		to, err := date(r["end_date"])
		if err != nil {
			return "", err
		}
		services[r["service_id"]] = true
		calendars = append(calendars, []any{id, r["service_id"], from, to, days})
	}
	exceptions := [][]any{}
	for _, r := range tables["calendar_dates.txt"] {
		d, err := date(r["date"])
		if err != nil {
			return "", err
		}
		if !services[r["service_id"]] {
			services[r["service_id"]] = true
			calendars = append(calendars, []any{id, r["service_id"], d, d, make([]bool, 7)})
		}
		exceptions = append(exceptions, []any{id, r["service_id"], d, r["exception_type"] == "1"})
	}
	if e = copyRows("calendars", []string{"version", "id", "start_date", "end_date", "weekdays"}, calendars); e != nil {
		return "", e
	}
	if e = copyRows("calendar_dates", []string{"version", "service", "date", "added"}, exceptions); e != nil {
		return "", e
	}
	rows = rows[:0]
	for _, r := range tables["trips.txt"] {
		rows = append(rows, []any{id, r["trip_id"], r["route_id"], r["service_id"]})
	}
	if e = copyRows("trips", []string{"version", "id", "route", "service"}, rows); e != nil {
		return "", e
	}
	rows = rows[:0]
	for _, r := range tables["stop_times.txt"] {
		if r["departure_time"] == "" {
			continue
		}
		sec, err := ParseTime(r["departure_time"])
		if err != nil {
			return "", fmt.Errorf("stop time: %w", err)
		}
		seq, err := strconv.Atoi(r["stop_sequence"])
		if err != nil {
			return "", err
		}
		rows = append(rows, []any{id, r["trip_id"], r["stop_id"], int32(seq), int32(sec)})
	}
	if e = copyRows("stop_times", []string{"version", "trip", "stop", "sequence", "departure"}, rows); e != nil {
		return "", e
	}
	return id, tx.Commit(ctx)
}
