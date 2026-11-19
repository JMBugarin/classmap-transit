package transit

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
)

// Vehicle is one bus from the vehicle positions feed. Occupancy is what the agency reports, if anything.
type Vehicle struct {
	ID        string   `json:"id"`
	Label     string   `json:"label,omitempty"`
	Trip      string   `json:"trip,omitempty"`
	Route     string   `json:"route,omitempty"`
	Lat       float64  `json:"lat"`
	Lon       float64  `json:"lon"`
	Bearing   *float32 `json:"bearing,omitempty"`
	Time      *int64   `json:"time,omitempty"`
	Stop      string   `json:"stop,omitempty"`
	Status    string   `json:"status,omitempty"`
	Occupancy string   `json:"occupancy,omitempty"`
}

// Alert is one service alert, with the periods it applies to (0 means open-ended).
type Alert struct {
	ID          string     `json:"id"`
	Header      string     `json:"header"`
	Description string     `json:"description,omitempty"`
	URL         string     `json:"url,omitempty"`
	Cause       string     `json:"cause,omitempty"`
	Effect      string     `json:"effect,omitempty"`
	Routes      []string   `json:"routes,omitempty"`
	Stops       []string   `json:"stops,omitempty"`
	Trips       []string   `json:"trips,omitempty"`
	Periods     [][2]int64 `json:"periods,omitempty"`
}

// LiveStatus says how old a live snapshot is, in the same terms as the departures feed.
type LiveStatus struct {
	Source    *int64     `json:"source_time"`
	Fetched   *time.Time `json:"fetched_at"`
	Freshness string     `json:"freshness"`
}

var occupancyNames = map[gtfs.VehiclePosition_OccupancyStatus]string{
	gtfs.VehiclePosition_EMPTY:                      "empty",
	gtfs.VehiclePosition_MANY_SEATS_AVAILABLE:       "many_seats",
	gtfs.VehiclePosition_FEW_SEATS_AVAILABLE:        "few_seats",
	gtfs.VehiclePosition_STANDING_ROOM_ONLY:         "standing",
	gtfs.VehiclePosition_CRUSHED_STANDING_ROOM_ONLY: "crushed",
	gtfs.VehiclePosition_FULL:                       "full",
	gtfs.VehiclePosition_NOT_ACCEPTING_PASSENGERS:   "not_accepting",
}

// Live keeps the latest vehicle positions and alerts in memory; nothing about riders is stored.
type Live struct {
	Now func() time.Time

	mu       sync.RWMutex
	vehicles []Vehicle
	vSource  *int64
	vFetched *time.Time
	alerts   []Alert
	aSource  *int64
	aFetched *time.Time
}

func NewLive() *Live { return &Live{Now: time.Now} }

func text(t *gtfs.TranslatedString) string {
	if t == nil {
		return ""
	}
	var first string
	for _, tr := range t.GetTranslation() {
		if first == "" {
			first = tr.GetText()
		}
		if l := strings.ToLower(tr.GetLanguage()); l == "en" || strings.HasPrefix(l, "en-") {
			return tr.GetText()
		}
	}
	return first
}

// SetVehicles replaces the vehicle snapshot from a raw feed.
func (l *Live) SetVehicles(raw []byte, at time.Time) error {
	m, e := Decode(raw)
	if e != nil {
		return e
	}
	out := []Vehicle{}
	for _, en := range m.GetEntity() {
		v := en.GetVehicle()
		if v == nil || v.GetPosition() == nil {
			continue
		}
		p := v.GetPosition()
		lat, lon := float64(p.GetLatitude()), float64(p.GetLongitude())
		if lat < -90 || lat > 90 || lon < -180 || lon > 180 || (lat == 0 && lon == 0) {
			continue
		}
		id := v.GetVehicle().GetId()
		if id == "" {
			id = en.GetId()
		}
		x := Vehicle{ID: id, Label: v.GetVehicle().GetLabel(), Trip: v.GetTrip().GetTripId(), Route: v.GetTrip().GetRouteId(), Lat: lat, Lon: lon, Stop: v.GetStopId()}
		if p.Bearing != nil {
			b := p.GetBearing()
			x.Bearing = &b
		}
		if v.Timestamp != nil {
			t := int64(v.GetTimestamp())
			x.Time = &t
		}
		if v.CurrentStatus != nil {
			x.Status = strings.ToLower(v.GetCurrentStatus().String())
		}
		if v.OccupancyStatus != nil {
			x.Occupancy = occupancyNames[v.GetOccupancyStatus()]
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	src := int64(m.GetHeader().GetTimestamp())
	l.mu.Lock()
	l.vehicles, l.vSource, l.vFetched = out, &src, &at
	l.mu.Unlock()
	return nil
}

// SetAlerts replaces the alert snapshot from a raw feed.
func (l *Live) SetAlerts(raw []byte, at time.Time) error {
	m, e := Decode(raw)
	if e != nil {
		return e
	}
	out := []Alert{}
	for _, en := range m.GetEntity() {
		a := en.GetAlert()
		if a == nil {
			continue
		}
		x := Alert{ID: en.GetId(), Header: text(a.GetHeaderText()), Description: text(a.GetDescriptionText()), URL: text(a.GetUrl())}
		if a.Cause != nil {
			x.Cause = strings.ToLower(a.GetCause().String())
		}
		if a.Effect != nil {
			x.Effect = strings.ToLower(a.GetEffect().String())
		}
		for _, s := range a.GetInformedEntity() {
			if r := s.GetRouteId(); r != "" {
				x.Routes = append(x.Routes, r)
			}
			if s2 := s.GetStopId(); s2 != "" {
				x.Stops = append(x.Stops, s2)
			}
			if t := s.GetTrip().GetTripId(); t != "" {
				x.Trips = append(x.Trips, t)
			}
		}
		for _, p := range a.GetActivePeriod() {
			x.Periods = append(x.Periods, [2]int64{int64(p.GetStart()), int64(p.GetEnd())})
		}
		if x.Header == "" && x.Description == "" {
			continue
		}
		out = append(out, x)
	}
	src := int64(m.GetHeader().GetTimestamp())
	l.mu.Lock()
	l.alerts, l.aSource, l.aFetched = out, &src, &at
	l.mu.Unlock()
	return nil
}

func active(a Alert, now int64) bool {
	if len(a.Periods) == 0 {
		return true
	}
	for _, p := range a.Periods {
		if (p[0] == 0 || p[0] <= now) && (p[1] == 0 || now <= p[1]) {
			return true
		}
	}
	return false
}

// Vehicles is the current snapshot and how fresh it is.
func (l *Live) Vehicles() (LiveStatus, []Vehicle) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	st := LiveStatus{Source: l.vSource, Fetched: l.vFetched, Freshness: Freshness(l.Now(), l.vSource)}
	if l.vehicles == nil || st.Freshness == "unavailable" {
		return st, []Vehicle{}
	}
	return st, l.vehicles
}

// Alerts is every alert in effect now.
func (l *Live) Alerts() (LiveStatus, []Alert) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	now := l.Now()
	st := LiveStatus{Source: l.aSource, Fetched: l.aFetched, Freshness: Freshness(now, l.aSource)}
	out := []Alert{}
	for _, a := range l.alerts {
		if active(a, now.Unix()) {
			out = append(out, a)
		}
	}
	return st, out
}

// PollLive keeps the vehicle and alert snapshots current.
func PollLive(ctx context.Context, l *Live, c *http.Client, vehiclesURL, alertsURL string, every time.Duration, logf func(string, ...any)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if vehiclesURL != "" {
			if b, e := Fetch(ctx, c, vehiclesURL); e != nil {
				logf("vehicles: %v", e)
			} else if e = l.SetVehicles(b, l.Now()); e != nil {
				logf("vehicles decode: %v", e)
			}
		}
		if alertsURL != "" {
			if b, e := Fetch(ctx, c, alertsURL); e != nil {
				logf("alerts: %v", e)
			} else if e = l.SetAlerts(b, l.Now()); e != nil {
				logf("alerts decode: %v", e)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
