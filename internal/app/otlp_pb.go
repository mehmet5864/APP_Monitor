package app

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
)

// OTLP/HTTP protobuf gövdesi için asgari ayrıştırıcı (yalnızca ExportTraceServiceRequest'in
// kullanılan alanları). Dış bağımlılık gerektirmez; bilinmeyen alanlar atlanır.

type pbSpan struct {
	TraceID, SpanID, ParentID string
	Name                      string
	Kind                      int
	StartNs, EndNs            uint64
	Status                    int
	StatusMsg                 string
	Attrs                     map[string]string
}
type pbResource struct {
	Attrs map[string]string
	Spans []pbSpan
}

var errPB = errors.New("geçersiz protobuf gövdesi")

type pbReader struct {
	b []byte
}

func (r *pbReader) done() bool { return len(r.b) == 0 }

func (r *pbReader) varint() (uint64, error) {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		return 0, errPB
	}
	r.b = r.b[n:]
	return v, nil
}

// next bir alan okur: numara, tel tipi ve (tel tipine göre) değer ya da bayt dilimi.
func (r *pbReader) next() (num int, wt int, v uint64, data []byte, err error) {
	k, err := r.varint()
	if err != nil {
		return
	}
	num, wt = int(k>>3), int(k&7)
	switch wt {
	case 0:
		v, err = r.varint()
	case 1:
		if len(r.b) < 8 {
			err = errPB
			return
		}
		v = binary.LittleEndian.Uint64(r.b)
		r.b = r.b[8:]
	case 2:
		var l uint64
		l, err = r.varint()
		if err != nil {
			return
		}
		if l > uint64(len(r.b)) {
			err = errPB
			return
		}
		data = r.b[:l]
		r.b = r.b[l:]
	case 5:
		if len(r.b) < 4 {
			err = errPB
			return
		}
		v = uint64(binary.LittleEndian.Uint32(r.b))
		r.b = r.b[4:]
	default:
		err = errPB
	}
	return
}

func pbKV(b []byte) (string, string, error) {
	r := &pbReader{b}
	var key, val string
	for !r.done() {
		num, wt, _, d, err := r.next()
		if err != nil {
			return "", "", err
		}
		switch {
		case num == 1 && wt == 2:
			key = string(d)
		case num == 2 && wt == 2: // AnyValue
			vr := &pbReader{d}
			for !vr.done() {
				n2, w2, v2, d2, err := vr.next()
				if err != nil {
					return "", "", err
				}
				switch {
				case n2 == 1 && w2 == 2:
					val = string(d2)
				case n2 == 2 && w2 == 0:
					val = strconv.FormatBool(v2 != 0)
				case n2 == 3 && w2 == 0:
					val = strconv.FormatInt(int64(v2), 10)
				case n2 == 4 && w2 == 1:
					val = strconv.FormatFloat(math.Float64frombits(v2), 'g', -1, 64)
				}
			}
		}
	}
	return key, val, nil
}

func pbSpanParse(b []byte) (pbSpan, error) {
	var sp pbSpan
	r := &pbReader{b}
	for !r.done() {
		num, wt, v, d, err := r.next()
		if err != nil {
			return sp, err
		}
		switch {
		case num == 1 && wt == 2:
			sp.TraceID = hex.EncodeToString(d)
		case num == 2 && wt == 2:
			sp.SpanID = hex.EncodeToString(d)
		case num == 4 && wt == 2:
			sp.ParentID = hex.EncodeToString(d)
		case num == 5 && wt == 2:
			sp.Name = string(d)
		case num == 6 && wt == 0:
			sp.Kind = int(v)
		case num == 7 && wt == 1:
			sp.StartNs = v
		case num == 8 && wt == 1:
			sp.EndNs = v
		case num == 9 && wt == 2:
			k, val, err := pbKV(d)
			if err != nil {
				return sp, err
			}
			if sp.Attrs == nil {
				sp.Attrs = map[string]string{}
			}
			if len(sp.Attrs) < 40 {
				sp.Attrs[k] = val
			}
		case num == 15 && wt == 2: // Status
			sr := &pbReader{d}
			for !sr.done() {
				n2, w2, v2, d2, err := sr.next()
				if err != nil {
					return sp, err
				}
				if n2 == 2 && w2 == 2 {
					sp.StatusMsg = string(d2)
				} else if n2 == 3 && w2 == 0 {
					sp.Status = int(v2)
				}
			}
		}
	}
	return sp, nil
}

// parseTracePB ExportTraceServiceRequest gövdesini ayrıştırır.
func parseTracePB(b []byte) ([]pbResource, error) {
	var out []pbResource
	r := &pbReader{b}
	for !r.done() {
		num, wt, _, d, err := r.next()
		if err != nil {
			return nil, err
		}
		if num != 1 || wt != 2 {
			continue
		}
		res := pbResource{Attrs: map[string]string{}}
		rr := &pbReader{d}
		for !rr.done() {
			n2, w2, _, d2, err := rr.next()
			if err != nil {
				return nil, err
			}
			if w2 != 2 {
				continue
			}
			switch n2 {
			case 1: // Resource
				xr := &pbReader{d2}
				for !xr.done() {
					n3, w3, _, d3, err := xr.next()
					if err != nil {
						return nil, err
					}
					if n3 == 1 && w3 == 2 {
						k, v, err := pbKV(d3)
						if err != nil {
							return nil, err
						}
						res.Attrs[k] = v
					}
				}
			case 2: // ScopeSpans
				sr := &pbReader{d2}
				for !sr.done() {
					n3, w3, _, d3, err := sr.next()
					if err != nil {
						return nil, err
					}
					if n3 == 2 && w3 == 2 {
						sp, err := pbSpanParse(d3)
						if err != nil {
							return nil, err
						}
						res.Spans = append(res.Spans, sp)
					}
				}
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// ---- OTLP günlük kayıtları (ExportLogsServiceRequest) ----

type pbLog struct {
	TimeNs, ObsNs   uint64
	Severity        int
	SeverityText    string
	Body            string
	TraceID, SpanID string
	Attrs           map[string]string
}
type pbLogRes struct {
	Attrs map[string]string
	Logs  []pbLog
}

func pbLogParse(b []byte) (pbLog, error) {
	var lg pbLog
	r := &pbReader{b}
	for !r.done() {
		num, wt, v, d, err := r.next()
		if err != nil {
			return lg, err
		}
		switch {
		case num == 1 && wt == 1:
			lg.TimeNs = v
		case num == 11 && wt == 1:
			lg.ObsNs = v
		case num == 2 && wt == 0:
			lg.Severity = int(v)
		case num == 3 && wt == 2:
			lg.SeverityText = string(d)
		case num == 5 && wt == 2: // AnyValue
			vr := &pbReader{d}
			for !vr.done() {
				n2, w2, _, d2, err := vr.next()
				if err != nil {
					return lg, err
				}
				if n2 == 1 && w2 == 2 {
					lg.Body = string(d2)
				}
			}
		case num == 6 && wt == 2:
			k, val, err := pbKV(d)
			if err != nil {
				return lg, err
			}
			if lg.Attrs == nil {
				lg.Attrs = map[string]string{}
			}
			if len(lg.Attrs) < 40 {
				lg.Attrs[k] = val
			}
		case num == 9 && wt == 2:
			lg.TraceID = hex.EncodeToString(d)
		case num == 10 && wt == 2:
			lg.SpanID = hex.EncodeToString(d)
		}
	}
	return lg, nil
}

// parseLogsPB ExportLogsServiceRequest gövdesini ayrıştırır.
func parseLogsPB(b []byte) ([]pbLogRes, error) {
	var out []pbLogRes
	r := &pbReader{b}
	for !r.done() {
		num, wt, _, d, err := r.next()
		if err != nil {
			return nil, err
		}
		if num != 1 || wt != 2 {
			continue
		}
		res := pbLogRes{Attrs: map[string]string{}}
		rr := &pbReader{d}
		for !rr.done() {
			n2, w2, _, d2, err := rr.next()
			if err != nil {
				return nil, err
			}
			if w2 != 2 {
				continue
			}
			switch n2 {
			case 1: // Resource
				xr := &pbReader{d2}
				for !xr.done() {
					n3, w3, _, d3, err := xr.next()
					if err != nil {
						return nil, err
					}
					if n3 == 1 && w3 == 2 {
						k, v, err := pbKV(d3)
						if err != nil {
							return nil, err
						}
						if len(res.Attrs) < 40 {
							res.Attrs[k] = v
						}
					}
				}
			case 2: // ScopeLogs
				sr := &pbReader{d2}
				for !sr.done() {
					n3, w3, _, d3, err := sr.next()
					if err != nil {
						return nil, err
					}
					if n3 == 2 && w3 == 2 {
						lg, err := pbLogParse(d3)
						if err != nil {
							return nil, err
						}
						res.Logs = append(res.Logs, lg)
					}
				}
			}
		}
		out = append(out, res)
	}
	return out, nil
}
