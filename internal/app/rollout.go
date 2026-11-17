package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/lib/pq"
)

// Kademeli ajan güncellemesi (C3): "Tümünü güncelle" 500 ajanı aynı anda yeniden başlatmak yerine dalgalar halinde ilerler.
// Her dalga için agent_update_req işaretlenir; dalganın tamamı güncellenince (veya zaman aşımına uğrayınca) sonraki dalga başlar.
// Dalgada hata varsa (halt_on_fail) kuyruk duraklar; yönetici devam ettirir veya iptal eder.

const rolloutWaveTimeout = 15 * time.Minute

type rolloutRow struct {
	ID          int64      `json:"id"`
	State       string     `json:"state"`
	Batch       int        `json:"batch"`
	HaltOnFail  bool       `json:"halt_on_fail"`
	Total       int        `json:"total"`
	Done        int        `json:"done"`
	Failed      int        `json:"failed"`
	Skipped     int        `json:"skipped"`
	Queued      int        `json:"queued"`
	InWave      int        `json:"in_wave"`
	WaveNo      int        `json:"wave_no"`
	WaveStarted *time.Time `json:"wave_started,omitempty"`
	Note        string     `json:"note"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
}

const rolloutCols = `id, state, batch, halt_on_fail, total, done, failed, skipped, cardinality(queue), cardinality(wave), wave_no, wave_started, note, created_by, created_at`

func scanRollout(sc interface{ Scan(...any) error }) (rolloutRow, error) {
	var r rolloutRow
	var ws sql.NullTime
	err := sc.Scan(&r.ID, &r.State, &r.Batch, &r.HaltOnFail, &r.Total, &r.Done, &r.Failed, &r.Skipped, &r.Queued, &r.InWave, &r.WaveNo, &ws, &r.Note, &r.CreatedBy, &r.CreatedAt)
	if ws.Valid {
		r.WaveStarted = &ws.Time
	}
	return r, err
}

// agentUpdateState bir ajanın güncelleme durumunu verir: done (sunucudaki dosyayla aynı), failed (ajan hata bildirdi), pending.
func (s *Server) agentUpdateState(labels map[string]string) (done, failed bool) {
	if b, ok := s.agentBinary(labels["os"], labels["arch"]); ok && b.SHA256 == labels["agent_sha"] {
		return true, false
	}
	return false, labels["update_error"] != ""
}

// POST /api/agent/rollout (yönetici): {"asset_ids":[…]|"all":true, "batch":25, "halt_on_fail":true}
func (s *Server) rolloutStart(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		AssetIDs   []int64 `json:"asset_ids"`
		All        bool    `json:"all"`
		Batch      int     `json:"batch"`
		HaltOnFail *bool   `json:"halt_on_fail"`
	}
	if !decode(w, r, &req) {
		return
	}
	if s.AgentDir == "" {
		fail(w, 400, "bu sunucuda ajan dosyası yok (APM_AGENT_DIR); sunucu paketiyle gelir")
		return
	}
	if !req.All && len(req.AssetIDs) == 0 {
		fail(w, 400, "asset_ids veya all gerekli")
		return
	}
	if len(req.AssetIDs) > 5000 {
		fail(w, 400, "en fazla 5000 asset")
		return
	}
	batch := clampInt(req.Batch, 25, 1, 200)
	halt := req.HaltOnFail == nil || *req.HaltOnFail
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, labels, agent_update_req FROM assets WHERE tenant_id = $1 AND collection = 'agent' AND labels ? 'agent_sha' AND status <> 'offline'
		   AND ($2 OR id = ANY($3::bigint[])) ORDER BY name`, a.TenantID, req.All, pq.Array(req.AssetIDs))
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	var queue []int64
	for rows.Next() {
		var id int64
		var raw []byte
		var requested bool
		if rows.Scan(&id, &raw, &requested) != nil {
			continue
		}
		l := map[string]string{}
		_ = json.Unmarshal(raw, &l)
		if _, ok := s.agentBinary(l["os"], l["arch"]); !ok {
			continue
		}
		if done, _ := s.agentUpdateState(l); done || requested {
			continue
		}
		queue = append(queue, id)
	}
	rows.Close()
	if len(queue) == 0 {
		fail(w, 400, "güncellenecek çevrimiçi ajan yok (hepsi güncel, çevrimdışı veya zaten istek bekliyor)")
		return
	}
	var id int64
	err = s.DB.QueryRowContext(r.Context(),
		`INSERT INTO agent_rollouts (tenant_id, batch, halt_on_fail, queue, total, created_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		a.TenantID, batch, halt, pq.Array(queue), len(queue), a.Label).Scan(&id)
	if err != nil {
		if pe, ok := err.(*pq.Error); ok && pe.Code == "23505" {
			fail(w, 409, "devam eden bir kademeli güncelleme var; önce bitirin veya iptal edin")
			return
		}
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.rolloutStep(r.Context(), id)
	s.audit(r.Context(), a.TenantID, a.Label, "agent.rollout_start", strconv.Itoa(len(queue))+" ajan, dalga "+strconv.Itoa(batch))
	row, _ := scanRollout(s.DB.QueryRowContext(r.Context(), `SELECT `+rolloutCols+` FROM agent_rollouts WHERE id = $1`, id))
	writeJSON(w, 201, row)
}

// GET /api/agent/rollout: etkin (yoksa son) kademeli güncelleme; yoksa null.
func (s *Server) rolloutGet(w http.ResponseWriter, r *http.Request, a Auth) {
	row, err := scanRollout(s.DB.QueryRowContext(r.Context(),
		`SELECT `+rolloutCols+` FROM agent_rollouts WHERE tenant_id = $1 ORDER BY (state IN ('running','paused')) DESC, id DESC LIMIT 1`, a.TenantID))
	if err == sql.ErrNoRows {
		writeJSON(w, 200, nil)
		return
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	writeJSON(w, 200, row)
}

// POST /api/agent/rollout/{action}: pause | resume | cancel
func (s *Server) rolloutAction(w http.ResponseWriter, r *http.Request, a Auth) {
	act := r.PathValue("action")
	var id int64
	var wave []int64
	if err := s.DB.QueryRowContext(r.Context(),
		`SELECT id, wave FROM agent_rollouts WHERE tenant_id = $1 AND state IN ('running','paused')`, a.TenantID).Scan(&id, pq.Array(&wave)); err != nil {
		fail(w, 404, "etkin kademeli güncelleme yok")
		return
	}
	switch act {
	case "pause":
		s.DB.ExecContext(r.Context(), `UPDATE agent_rollouts SET state = 'paused', note = 'Elle duraklatıldı', updated_at = now() WHERE id = $1`, id)
	case "resume":
		s.DB.ExecContext(r.Context(), `UPDATE agent_rollouts SET state = 'running', note = '', updated_at = now() WHERE id = $1`, id)
		s.rolloutStep(r.Context(), id)
	case "cancel":
		s.DB.ExecContext(r.Context(), `UPDATE agent_rollouts SET state = 'canceled', queue = '{}', note = 'İptal edildi', updated_at = now() WHERE id = $1`, id)
		// henüz güncellenmemiş dalga üyelerinin isteğini geri al
		s.DB.ExecContext(r.Context(), `UPDATE assets SET agent_update_req = false WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, a.TenantID, pq.Array(wave))
	default:
		fail(w, 400, "geçersiz işlem")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "agent.rollout_"+act, strconv.FormatInt(id, 10))
	row, _ := scanRollout(s.DB.QueryRowContext(r.Context(), `SELECT `+rolloutCols+` FROM agent_rollouts WHERE id = $1`, id))
	writeJSON(w, 200, row)
}

// rolloutJob etkin kademeli güncellemeleri ilerletir.
func (s *Server) rolloutJob(ctx context.Context) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM agent_rollouts WHERE state = 'running'`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		s.rolloutStep(ctx, id)
	}
}

// rolloutStep dalgayı değerlendirir; bittiyse sonrakini başlatır, hata varsa duraklatır, kuyruk bittiyse kapatır.
func (s *Server) rolloutStep(ctx context.Context, id int64) {
	var tenant int64
	var state string
	var batch, done, failed, skipped, waveNo int
	var halt bool
	var queue, wave []int64
	var ws sql.NullTime
	if err := s.DB.QueryRowContext(ctx,
		`SELECT tenant_id, state, batch, halt_on_fail, queue, wave, wave_no, wave_started, done, failed, skipped FROM agent_rollouts WHERE id = $1`, id).
		Scan(&tenant, &state, &batch, &halt, pq.Array(&queue), pq.Array(&wave), &waveNo, &ws, &done, &failed, &skipped); err != nil || state != "running" {
		return
	}
	save := func(st, nt string) {
		if queue == nil { // pq.Array(nil) NULL yazar; sütunlar NOT NULL
			queue = []int64{}
		}
		if wave == nil {
			wave = []int64{}
		}
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE agent_rollouts SET state = $2, note = $3, queue = $4, wave = $5, wave_no = $6, wave_started = $7, done = $8, failed = $9, skipped = $10, updated_at = now() WHERE id = $1`,
			id, st, nt, pq.Array(queue), pq.Array(wave), waveNo, ws, done, failed, skipped); err != nil {
			log.Printf("kademeli güncelleme kaydı: %v", err)
		}
	}
	waveFailed := 0
	if len(wave) > 0 {
		rows, err := s.DB.QueryContext(ctx, `SELECT id, labels, status FROM assets WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, tenant, pq.Array(wave))
		if err != nil {
			return
		}
		timedOut := ws.Valid && time.Since(ws.Time) > rolloutWaveTimeout
		var left, gaveUp []int64
		for rows.Next() {
			var aid int64
			var raw []byte
			var st string
			if rows.Scan(&aid, &raw, &st) != nil {
				continue
			}
			l := map[string]string{}
			_ = json.Unmarshal(raw, &l)
			d, f := s.agentUpdateState(l)
			switch {
			case d:
				done++
			case f:
				failed++
				waveFailed++
				gaveUp = append(gaveUp, aid)
			case timedOut && st == "offline":
				skipped++
				gaveUp = append(gaveUp, aid)
			case timedOut:
				failed++
				waveFailed++
				gaveUp = append(gaveUp, aid)
			default:
				left = append(left, aid)
			}
		}
		rows.Close()
		if len(gaveUp) > 0 { // hata veren/yanıt vermeyen ajan için istek geri alınır; ajan sürekli yeniden denemez
			s.DB.ExecContext(ctx, `UPDATE assets SET agent_update_req = false WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, tenant, pq.Array(gaveUp))
		}
		wave = left
		if waveFailed > 0 && halt {
			save("paused", strconv.Itoa(waveFailed)+" ajan güncellenemedi; kuyruk duraklatıldı. Hatayı inceleyip devam ettirin veya iptal edin.")
			return
		}
		if len(wave) > 0 {
			save("running", "") // dalganın kalanı beklenir
			return
		}
	}
	if len(queue) == 0 {
		save("done", "")
		return
	}
	n := batch
	if n > len(queue) {
		n = len(queue)
	}
	wave, queue = queue[:n], queue[n:]
	waveNo++
	if _, err := s.DB.ExecContext(ctx, `UPDATE assets SET agent_update_req = true WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, tenant, pq.Array(wave)); err != nil {
		log.Printf("kademeli güncelleme: %v", err)
		return
	}
	ws = sql.NullTime{Time: time.Now(), Valid: true}
	save("running", "")
}
