// Package export writes Domestique's data back out in standard formats, so it
// never locks in: FIT activity files generated from the canonical merged
// stream, CSV of streams, sets and activity summaries, and file names safe for
// any download.
package export

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"

	"domestique/internal/activity"
)

// A minimal FIT activity encoder. tormoder/fit can encode, but it writes each
// message type as one block in struct order (activity summary first, the timer
// start event after every record) and builds the file by reflection. Garmin's
// cookbook recommends chronological order with the summary last, which every
// importer (Strava, Garmin Connect, intervals.icu) accepts, so this writes:
//
//	file_id, event (timer start), record..., event (timer stop), lap..., session, activity, CRC
//
// The output is verified by decoding it with tormoder/fit in the tests.

// Lap is one lap as offsets (seconds) into the stream's timeline: [Start, End).
type Lap struct {
	Start, End int32
}

// FITActivity is everything WriteFIT needs.
type FITActivity struct {
	Start    time.Time
	Sport    string // activity.Sport*
	Stream   *activity.Stream
	ElapsedS int
	MovingS  int
	// DistanceM is the activity total; 0 means take it from the stream.
	DistanceM float64
	AscentM   float64
	KJ        float64
	NP        int
	TSS       float64
	IF        float64
	FTP       int
	// Laps, in order. Empty means one lap for the whole activity.
	Laps []Lap
	// Loc is the athlete's zone, for the activity's local timestamp.
	Loc *time.Location
}

// FIT epoch: 1989-12-31T00:00:00Z.
const fitEpoch = 631065600

func fitTime(t time.Time) uint32 { return uint32(t.Unix() - fitEpoch) }

// Base types.
const (
	tEnum   = 0x00
	tUint8  = 0x02
	tUint16 = 0x84
	tSint32 = 0x85
	tUint32 = 0x86
)

// Global message numbers.
const (
	mFileID   = 0
	mSession  = 18
	mLap      = 19
	mRecord   = 20
	mEvent    = 21
	mActivity = 34
)

type fieldDef struct {
	num, size, base byte
}

type fitWriter struct {
	buf bytes.Buffer
	err error
}

func (w *fitWriter) define(local byte, global uint16, fields []fieldDef) {
	w.buf.WriteByte(0x40 | local)
	w.buf.WriteByte(0) // reserved
	w.buf.WriteByte(0) // little-endian
	w.u16(global)      //
	w.buf.WriteByte(byte(len(fields)))
	for _, f := range fields {
		w.buf.Write([]byte{f.num, f.size, f.base})
	}
}

func (w *fitWriter) u8(v uint8)   { w.buf.WriteByte(v) }
func (w *fitWriter) u16(v uint16) { _ = binary.Write(&w.buf, binary.LittleEndian, v) }
func (w *fitWriter) u32(v uint32) { _ = binary.Write(&w.buf, binary.LittleEndian, v) }
func (w *fitWriter) s32(v int32)  { _ = binary.Write(&w.buf, binary.LittleEndian, v) }

const (
	inv8   = 0xFF
	inv16  = 0xFFFF
	inv32  = 0xFFFFFFFF
	invS32 = 0x7FFFFFFF
)

func clampU8(v float64) uint8 {
	if math.IsNaN(v) || v < 0 || v >= inv8 {
		return inv8
	}
	return uint8(math.Round(v))
}

func clampU16(v float64) uint16 {
	if math.IsNaN(v) || v < 0 || v >= inv16 {
		return inv16
	}
	return uint16(math.Round(v))
}

func clampU32(v float64) uint32 {
	if math.IsNaN(v) || v < 0 || v >= inv32 {
		return inv32
	}
	return uint32(math.Round(v))
}

func semicircles(deg float64) int32 { return int32(math.Round(deg * (1 << 31) / 180)) }

// fitSport maps a Domestique sport to FIT sport and sub-sport.
func fitSport(s string) (sport, sub uint8) {
	switch s {
	case activity.SportRide:
		return 2, 0 // cycling, generic
	case activity.SportVirtualRide:
		return 2, 58 // cycling, virtual_activity
	case activity.SportRun:
		return 1, 0
	case activity.SportWalk:
		return 11, 0
	case activity.SportHike:
		return 17, 0
	case activity.SportSwim:
		return 5, 0
	case activity.SportStrength:
		return 10, 20 // training, strength_training
	}
	return 0, 0
}

// summary is what a lap or session reports about its span of the stream.
type summary struct {
	startIdx, endIdx   int // [startIdx, endIdx) into the stream
	start, end         int32
	timer              float64
	dist               float64
	avgSpeed, maxSpeed float64
	avgHR, maxHR       float64
	avgCad, maxCad     float64
	avgPower, maxPower float64
	ascent, descent    float64
	startLat, startLng float64
	endLat, endLng     float64
	hasPos, hasEndPos  bool
}

func summarize(s *activity.Stream, startIdx, endIdx int) summary {
	x := summary{startIdx: startIdx, endIdx: endIdx, avgSpeed: math.NaN(), maxSpeed: math.NaN(),
		avgHR: math.NaN(), maxHR: math.NaN(), avgCad: math.NaN(), maxCad: math.NaN(),
		avgPower: math.NaN(), maxPower: math.NaN(), ascent: math.NaN(), descent: math.NaN()}
	if endIdx <= startIdx {
		return x
	}
	x.start, x.end = s.T[startIdx], s.T[endIdx-1]
	mean := func(v []int32) (avg, mx float64) {
		if v == nil {
			return math.NaN(), math.NaN()
		}
		var sum, n float64
		mx = math.NaN()
		for i := startIdx; i < endIdx; i++ {
			if v[i] < 0 {
				continue
			}
			sum += float64(v[i])
			n++
			if math.IsNaN(mx) || float64(v[i]) > mx {
				mx = float64(v[i])
			}
		}
		if n == 0 {
			return math.NaN(), math.NaN()
		}
		return sum / n, mx
	}
	x.avgHR, x.maxHR = mean(s.HR)
	x.avgPower, x.maxPower = mean(s.Power)
	x.avgCad, x.maxCad = mean(s.Cadence)
	if s.Distance != nil {
		first, last := float64(s.Distance[startIdx]), float64(s.Distance[endIdx-1])
		if startIdx > 0 {
			first = float64(s.Distance[startIdx-1])
		}
		x.dist = math.Max(0, last-first)
	}
	if s.Speed != nil {
		x.maxSpeed = 0
		for i := startIdx; i < endIdx; i++ {
			x.maxSpeed = math.Max(x.maxSpeed, float64(s.Speed[i]))
		}
	}
	if s.Altitude != nil {
		x.ascent, x.descent = 0, 0
		for i := startIdx + 1; i < endIdx; i++ {
			d := float64(s.Altitude[i] - s.Altitude[i-1])
			if d > 0 {
				x.ascent += d
			} else {
				x.descent -= d
			}
		}
	}
	if s.Lat != nil {
		for i := startIdx; i < endIdx; i++ {
			if s.Lat[i] != 0 || s.Lng[i] != 0 {
				if !x.hasPos {
					x.startLat, x.startLng, x.hasPos = s.Lat[i], s.Lng[i], true
				}
				x.endLat, x.endLng, x.hasEndPos = s.Lat[i], s.Lng[i], true
			}
		}
	}
	return x
}

// WriteFIT encodes a as a FIT activity file. The file is assembled in memory
// (the header carries the data size): about 30 bytes a second, so 600 KB for
// a five-hour ride.
func WriteFIT(out io.Writer, a FITActivity) error {
	s := a.Stream
	if s == nil || s.Len() == 0 {
		return errors.New("no samples to export")
	}
	n := s.Len()
	loc := a.Loc
	if loc == nil {
		loc = time.UTC
	}
	at := func(off int32) uint32 { return fitTime(a.Start.Add(time.Duration(off) * time.Second)) }
	last := s.T[n-1]
	elapsed := float64(a.ElapsedS)
	if elapsed < float64(last) {
		elapsed = float64(last)
	}
	moving := float64(a.MovingS)
	if moving <= 0 || moving > elapsed {
		moving = elapsed
	}
	sport, sub := fitSport(a.Sport)

	w := &fitWriter{}

	// file_id
	w.define(0, mFileID, []fieldDef{{0, 1, tEnum}, {1, 2, tUint16}, {2, 2, tUint16}, {3, 4, 0x8C}, {4, 4, tUint32}})
	w.u8(0)
	w.u8(4)                       // type: activity
	w.u16(255)                    // manufacturer: development
	w.u16(0)                      // product
	w.u32(uint32(a.Start.Unix())) // serial number: stable per activity
	w.u32(fitTime(a.Start))

	// event: timer start / stop
	eventFields := []fieldDef{{253, 4, tUint32}, {0, 1, tEnum}, {1, 1, tEnum}, {4, 1, tUint8}}
	w.define(1, mEvent, eventFields)
	w.u8(1)
	w.u32(fitTime(a.Start))
	w.u8(0) // event: timer
	w.u8(0) // type: start
	w.u8(0)

	// record definition carries only the channels the stream has.
	rf := []fieldDef{{253, 4, tUint32}}
	hasGPS := s.HasGPS()
	if hasGPS {
		rf = append(rf, fieldDef{0, 4, tSint32}, fieldDef{1, 4, tSint32})
	}
	if s.Altitude != nil {
		rf = append(rf, fieldDef{2, 2, tUint16})
	}
	if s.HR != nil {
		rf = append(rf, fieldDef{3, 1, tUint8})
	}
	if s.Cadence != nil {
		rf = append(rf, fieldDef{4, 1, tUint8})
	}
	if s.Distance != nil {
		rf = append(rf, fieldDef{5, 4, tUint32})
	}
	if s.Speed != nil {
		rf = append(rf, fieldDef{6, 2, tUint16})
	}
	if s.Power != nil {
		rf = append(rf, fieldDef{7, 2, tUint16})
	}
	w.define(2, mRecord, rf)
	intv := func(v []int32, i int) float64 {
		if v[i] < 0 {
			return math.NaN()
		}
		return float64(v[i])
	}
	for i := 0; i < n; i++ {
		w.u8(2)
		w.u32(at(s.T[i]))
		if hasGPS {
			if s.Lat[i] == 0 && s.Lng[i] == 0 {
				w.s32(invS32)
				w.s32(invS32)
			} else {
				w.s32(semicircles(s.Lat[i]))
				w.s32(semicircles(s.Lng[i]))
			}
		}
		if s.Altitude != nil {
			w.u16(clampU16((float64(s.Altitude[i]) + 500) * 5))
		}
		if s.HR != nil {
			w.u8(clampU8(intv(s.HR, i)))
		}
		if s.Cadence != nil {
			w.u8(clampU8(intv(s.Cadence, i)))
		}
		if s.Distance != nil {
			w.u32(clampU32(float64(s.Distance[i]) * 100))
		}
		if s.Speed != nil {
			w.u16(clampU16(float64(s.Speed[i]) * 1000))
		}
		if s.Power != nil {
			w.u16(clampU16(intv(s.Power, i)))
		}
	}

	endAt := at(last)
	w.u8(1)
	w.u32(endAt)
	w.u8(0) // timer
	w.u8(4) // stop_all
	w.u8(0)

	// Laps: map offset spans to sample indexes; fall back to one lap.
	spans := lapIndexes(s, a.Laps)
	sumFields := func(lap bool) []fieldDef {
		f := []fieldDef{
			{254, 2, tUint16}, {253, 4, tUint32}, {0, 1, tEnum}, {1, 1, tEnum}, {2, 4, tUint32},
			{3, 4, tSint32}, {4, 4, tSint32},
			{7, 4, tUint32}, {8, 4, tUint32}, {9, 4, tUint32}, {11, 2, tUint16},
		}
		if lap {
			f = append(f, fieldDef{5, 4, tSint32}, fieldDef{6, 4, tSint32},
				fieldDef{13, 2, tUint16}, fieldDef{14, 2, tUint16}, fieldDef{15, 1, tUint8}, fieldDef{16, 1, tUint8},
				fieldDef{17, 1, tUint8}, fieldDef{18, 1, tUint8}, fieldDef{19, 2, tUint16}, fieldDef{20, 2, tUint16},
				fieldDef{21, 2, tUint16}, fieldDef{22, 2, tUint16}, fieldDef{24, 1, tEnum}, fieldDef{25, 1, tEnum},
				fieldDef{39, 1, tEnum})
		} else {
			f = append(f, fieldDef{5, 1, tEnum}, fieldDef{6, 1, tEnum},
				fieldDef{14, 2, tUint16}, fieldDef{15, 2, tUint16}, fieldDef{16, 1, tUint8}, fieldDef{17, 1, tUint8},
				fieldDef{18, 1, tUint8}, fieldDef{19, 1, tUint8}, fieldDef{20, 2, tUint16}, fieldDef{21, 2, tUint16},
				fieldDef{22, 2, tUint16}, fieldDef{23, 2, tUint16}, fieldDef{25, 2, tUint16}, fieldDef{26, 2, tUint16},
				fieldDef{28, 1, tEnum}, fieldDef{34, 2, tUint16}, fieldDef{35, 2, tUint16}, fieldDef{36, 2, tUint16},
				fieldDef{45, 2, tUint16})
		}
		return f
	}
	pos := func(ok bool, lat, lng float64) {
		if ok {
			w.s32(semicircles(lat))
			w.s32(semicircles(lng))
		} else {
			w.s32(invS32)
			w.s32(invS32)
		}
	}
	speed := func(dist, secs float64) float64 {
		if secs <= 0 || dist <= 0 {
			return math.NaN()
		}
		return dist / secs * 1000
	}
	w.define(3, mLap, sumFields(true))
	for i, sp := range spans {
		x := summarize(s, sp[0], sp[1])
		lapElapsed := float64(x.end - x.start)
		if i+1 < len(spans) {
			lapElapsed = float64(s.T[spans[i+1][0]] - x.start)
		}
		lapElapsed = math.Max(lapElapsed, 1)
		// Timer time: the share of moving time this lap's span holds.
		lapTimer := math.Min(lapElapsed, lapElapsed*moving/math.Max(elapsed, 1))
		lapEnd := at(x.start + int32(lapElapsed))
		if i == len(spans)-1 {
			lapEnd = endAt
		}
		w.u8(3)
		w.u16(uint16(i))
		w.u32(lapEnd)
		w.u8(9) // event: lap
		w.u8(1) // stop
		w.u32(at(x.start))
		pos(x.hasPos, x.startLat, x.startLng)
		w.u32(clampU32(lapElapsed * 1000))
		w.u32(clampU32(lapTimer * 1000))
		w.u32(clampU32(x.dist * 100))
		w.u16(inv16) // calories
		pos(x.hasEndPos, x.endLat, x.endLng)
		w.u16(clampU16(speed(x.dist, lapTimer)))
		w.u16(clampU16(x.maxSpeed * 1000))
		w.u8(clampU8(x.avgHR))
		w.u8(clampU8(x.maxHR))
		w.u8(clampU8(x.avgCad))
		w.u8(clampU8(x.maxCad))
		w.u16(clampU16(x.avgPower))
		w.u16(clampU16(x.maxPower))
		w.u16(clampU16(x.ascent))
		w.u16(clampU16(x.descent))
		if i == len(spans)-1 {
			w.u8(7) // lap_trigger: session_end
		} else {
			w.u8(0) // manual
		}
		w.u8(sport)
		w.u8(sub)
	}

	all := summarize(s, 0, n)
	dist := a.DistanceM
	if dist <= 0 {
		dist = all.dist
	}
	ascent := all.ascent
	if a.AscentM > 0 {
		ascent = a.AscentM
	}
	cal := math.NaN()
	if a.KJ > 0 {
		cal = a.KJ // gross efficiency ~24%: kcal burned ≈ kJ of work
	}
	nan0 := func(v float64) float64 {
		if v <= 0 {
			return math.NaN()
		}
		return v
	}
	w.define(4, mSession, sumFields(false))
	w.u8(4)
	w.u16(0)
	w.u32(endAt)
	w.u8(8) // event: session
	w.u8(1) // stop
	w.u32(fitTime(a.Start))
	pos(all.hasPos, all.startLat, all.startLng)
	w.u32(clampU32(elapsed * 1000))
	w.u32(clampU32(moving * 1000))
	w.u32(clampU32(dist * 100))
	w.u16(clampU16(cal))
	w.u8(sport)
	w.u8(sub)
	w.u16(clampU16(speed(dist, moving)))
	w.u16(clampU16(all.maxSpeed * 1000))
	w.u8(clampU8(all.avgHR))
	w.u8(clampU8(all.maxHR))
	w.u8(clampU8(all.avgCad))
	w.u8(clampU8(all.maxCad))
	w.u16(clampU16(all.avgPower))
	w.u16(clampU16(all.maxPower))
	w.u16(clampU16(ascent))
	w.u16(clampU16(all.descent))
	w.u16(0)                  // first_lap_index
	w.u16(uint16(len(spans))) // num_laps
	w.u8(0)                   // trigger: activity_end
	w.u16(clampU16(nan0(float64(a.NP))))
	w.u16(clampU16(nan0(a.TSS * 10)))
	w.u16(clampU16(nan0(a.IF * 1000)))
	w.u16(clampU16(nan0(float64(a.FTP))))

	// activity
	w.define(5, mActivity, []fieldDef{{253, 4, tUint32}, {0, 4, tUint32}, {1, 2, tUint16}, {2, 1, tEnum},
		{3, 1, tEnum}, {4, 1, tEnum}, {5, 4, tUint32}})
	end := a.Start.Add(time.Duration(last) * time.Second)
	_, offset := end.In(loc).Zone()
	w.u8(5)
	w.u32(endAt)
	w.u32(clampU32(moving * 1000))
	w.u16(1) // num_sessions
	w.u8(0)  // type: manual
	w.u8(26) // event: activity
	w.u8(1)  // stop
	w.u32(uint32(int64(endAt) + int64(offset)))

	if w.err != nil {
		return w.err
	}
	data := w.buf.Bytes()
	hdr := make([]byte, 14)
	hdr[0] = 14
	hdr[1] = 0x10 // protocol 1.0: nothing here needs 2.0
	binary.LittleEndian.PutUint16(hdr[2:], 2132)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(data)))
	copy(hdr[8:], ".FIT")
	binary.LittleEndian.PutUint16(hdr[12:], crc16(0, hdr[:12]))
	crc := crc16(crc16(0, hdr), data)
	if _, err := out.Write(hdr); err != nil {
		return err
	}
	if _, err := out.Write(data); err != nil {
		return err
	}
	var tail [2]byte
	binary.LittleEndian.PutUint16(tail[:], crc)
	_, err := out.Write(tail[:])
	return err
}

// lapIndexes turns offset spans into contiguous [start, end) sample ranges
// covering the whole stream. Spans that hold no samples are dropped.
func lapIndexes(s *activity.Stream, laps []Lap) [][2]int {
	n := s.Len()
	if len(laps) < 2 {
		return [][2]int{{0, n}}
	}
	var bounds []int
	for _, l := range laps[1:] {
		// first sample at or after the lap's start
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			if s.T[m] < l.Start {
				lo = m + 1
			} else {
				hi = m
			}
		}
		if lo > 0 && lo < n && (len(bounds) == 0 || lo > bounds[len(bounds)-1]) {
			bounds = append(bounds, lo)
		}
	}
	out := make([][2]int, 0, len(bounds)+1)
	prev := 0
	for _, b := range bounds {
		out = append(out, [2]int{prev, b})
		prev = b
	}
	return append(out, [2]int{prev, n})
}

var crcTable = [16]uint16{
	0x0000, 0xCC01, 0xD801, 0x1400, 0xF001, 0x3C00, 0x2800, 0xE401,
	0xA001, 0x6C00, 0x7800, 0xB401, 0x5000, 0x9C01, 0x8801, 0x4400,
}

func crc16(crc uint16, data []byte) uint16 {
	for _, b := range data {
		tmp := crcTable[crc&0xF]
		crc = (crc >> 4) & 0x0FFF
		crc = crc ^ tmp ^ crcTable[b&0xF]
		tmp = crcTable[crc&0xF]
		crc = (crc >> 4) & 0x0FFF
		crc = crc ^ tmp ^ crcTable[(b>>4)&0xF]
	}
	return crc
}
