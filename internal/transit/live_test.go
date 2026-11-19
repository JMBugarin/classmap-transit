package transit

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"google.golang.org/protobuf/proto"
)

func feed(t *testing.T, at time.Time, entities ...*gtfs.FeedEntity) []byte {
	t.Helper()
	b, e := proto.Marshal(&gtfs.FeedMessage{Header: &gtfs.FeedHeader{GtfsRealtimeVersion: proto.String("2.0"), Timestamp: proto.Uint64(uint64(at.Unix()))}, Entity: entities})
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestLiveVehicles(t *testing.T) {
	now := time.Date(2026, 11, 18, 18, 0, 0, 0, time.UTC)
	l := NewLive()
	l.Now = func() time.Time { return now }
	if st, vs := l.Vehicles(); st.Freshness != "unavailable" || len(vs) != 0 {
		t.Fatal("empty live data should be unavailable", st, vs)
	}
	few := gtfs.VehiclePosition_FEW_SEATS_AVAILABLE
	stopped := gtfs.VehiclePosition_STOPPED_AT
	raw := feed(t, now.Add(-20*time.Second),
		&gtfs.FeedEntity{Id: proto.String("e1"), Vehicle: &gtfs.VehiclePosition{
			Trip:            &gtfs.TripDescriptor{TripId: proto.String("trip-18"), RouteId: proto.String("18")},
			Vehicle:         &gtfs.VehicleDescriptor{Id: proto.String("2214"), Label: proto.String("2214")},
			Position:        &gtfs.Position{Latitude: proto.Float32(36.9995), Longitude: proto.Float32(-122.0628), Bearing: proto.Float32(90)},
			Timestamp:       proto.Uint64(uint64(now.Add(-25 * time.Second).Unix())),
			StopId:          proto.String("1594"),
			CurrentStatus:   &stopped,
			OccupancyStatus: &few,
		}},
		&gtfs.FeedEntity{Id: proto.String("e2"), Vehicle: &gtfs.VehiclePosition{Vehicle: &gtfs.VehicleDescriptor{Id: proto.String("no-position")}}},
		&gtfs.FeedEntity{Id: proto.String("e3"), Vehicle: &gtfs.VehiclePosition{Vehicle: &gtfs.VehicleDescriptor{Id: proto.String("null-island")}, Position: &gtfs.Position{Latitude: proto.Float32(0), Longitude: proto.Float32(0)}}},
	)
	if e := l.SetVehicles(raw, now); e != nil {
		t.Fatal(e)
	}
	st, vs := l.Vehicles()
	if st.Freshness != "fresh" || len(vs) != 1 {
		t.Fatalf("got %s with %d vehicles", st.Freshness, len(vs))
	}
	v := vs[0]
	if v.ID != "2214" || v.Trip != "trip-18" || v.Route != "18" || v.Occupancy != "few_seats" || v.Status != "stopped_at" || v.Stop != "1594" || v.Bearing == nil || *v.Bearing != 90 {
		t.Fatalf("vehicle %+v", v)
	}
	if e := l.SetVehicles([]byte("not a feed"), now); e == nil {
		t.Fatal("accepted garbage")
	}
	if _, vs = l.Vehicles(); len(vs) != 1 {
		t.Fatal("a bad fetch replaced the last good snapshot")
	}
	now = now.Add(3 * time.Minute)
	if st, _ = l.Vehicles(); st.Freshness != "stale" {
		t.Fatalf("old snapshot is %s", st.Freshness)
	}

	a := &API{Live: l}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/v1/vehicles", nil))
	var body struct {
		Status   LiveStatus `json:"status"`
		Vehicles []Vehicle  `json:"vehicles"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Status.Freshness != "stale" || len(body.Vehicles) != 1 {
		t.Fatalf("GET /v1/vehicles: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	(&API{}).ServeHTTP(w, httptest.NewRequest("GET", "/v1/vehicles", nil))
	if w.Code != 503 {
		t.Fatalf("without live data: %d", w.Code)
	}
}

func TestLiveAlerts(t *testing.T) {
	now := time.Date(2026, 11, 18, 18, 0, 0, 0, time.UTC)
	l := NewLive()
	l.Now = func() time.Time { return now }
	tr := func(pairs ...string) *gtfs.TranslatedString {
		var out []*gtfs.TranslatedString_Translation
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, &gtfs.TranslatedString_Translation{Language: proto.String(pairs[i]), Text: proto.String(pairs[i+1])})
		}
		return &gtfs.TranslatedString{Translation: out}
	}
	detour := gtfs.Alert_DETOUR
	raw := feed(t, now,
		&gtfs.FeedEntity{Id: proto.String("now"), Alert: &gtfs.Alert{
			HeaderText:     tr("es", "Desvío", "en", "Route 18 detour"),
			Effect:         &detour,
			InformedEntity: []*gtfs.EntitySelector{{RouteId: proto.String("18")}, {StopId: proto.String("1594")}},
			ActivePeriod:   []*gtfs.TimeRange{{Start: proto.Uint64(uint64(now.Add(-time.Hour).Unix()))}},
		}},
		&gtfs.FeedEntity{Id: proto.String("past"), Alert: &gtfs.Alert{
			HeaderText:   tr("en", "Yesterday's closure"),
			ActivePeriod: []*gtfs.TimeRange{{Start: proto.Uint64(1), End: proto.Uint64(uint64(now.Add(-time.Hour).Unix()))}},
		}},
		&gtfs.FeedEntity{Id: proto.String("blank"), Alert: &gtfs.Alert{}},
	)
	if e := l.SetAlerts(raw, now); e != nil {
		t.Fatal(e)
	}
	st, as := l.Alerts()
	if st.Freshness != "fresh" || len(as) != 1 {
		t.Fatalf("got %s with %+v", st.Freshness, as)
	}
	if as[0].Header != "Route 18 detour" || as[0].Effect != "detour" || len(as[0].Routes) != 1 || as[0].Stops[0] != "1594" {
		t.Fatalf("alert %+v", as[0])
	}
}
