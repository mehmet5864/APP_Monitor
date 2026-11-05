package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"syscall"
	"time"

	"github.com/lib/pq"

	"appmon/internal/notify"
	"appmon/internal/probe"
)

var sevRank = map[string]int{"info": 1, "warning": 2, "critical": 3}

// inMaintenance asset'in şu an bir bakım penceresinde olup olmadığını söyler (alarm açılır ama bildirim gitmez).
func (s *Server) inMaintenance(ctx context.Context, tenant, asset int64) bool {
	var n int
	err := s.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM maintenance_windows w JOIN assets a ON a.id = $2 AND a.tenant_id = w.tenant_id
		WHERE w.tenant_id = $1
		  AND (cardinality(w.asset_ids) = 0 AND w.group_name = '' AND w.tag = '' OR a.id = ANY(w.asset_ids)
		       OR (w.group_name <> '' AND a.grp = w.group_name) OR (w.tag <> '' AND w.tag = ANY(a.tags)))
		  AND (
		    (w.repeat = '' AND now() BETWEEN w.starts_at AND w.ends_at)
		    OR (w.repeat = 'daily' AND now() >= w.starts_at AND
		        mod(extract(epoch FROM now() - w.starts_at)::bigint, 86400) <= extract(epoch FROM w.ends_at - w.starts_at))
		    OR (w.repeat = 'weekly' AND now() >= w.starts_at AND
		        mod(extract(epoch FROM now() - w.starts_at)::bigint, 604800) <= extract(epoch FROM w.ends_at - w.starts_at))
		  )`, tenant, asset).Scan(&n)
	if err != nil {
		log.Printf("bakım penceresi sorgusu: %v", err)
	}
	return err == nil && n > 0
}

func (s *Server) publicLink() string {
	if s.PublicURL == "" {
		return ""
	}
	return s.PublicURL + "/#alarmlar"
}

// dispatchAlert alarm açıldığında/düzeldiğinde uygun kanallara bildirim gönderir (arka planda).
func (s *Server) dispatchAlert(tenant int64, event string, asset int64, rule, sev string, val, thr float64, cmp string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var assetName string
		if s.DB.QueryRowContext(ctx, `SELECT name FROM assets WHERE id = $1`, asset).Scan(&assetName) != nil {
			return
		}
		subject := rule + " — " + assetName
		text := fmt.Sprintf("%s hizmetinde \"%s\" alarmı açıldı.", assetName, rule)
		if event == "resolved" {
			text = fmt.Sprintf("%s hizmetindeki \"%s\" alarmı düzeldi.", assetName, rule)
			subject = rule + " düzeldi — " + assetName
		}
		m := notify.Message{Event: event, Severity: sev, Subject: subject, Text: text, Asset: assetName, Rule: rule,
			Value: fmt.Sprintf("%s (%s %s)", strconv.FormatFloat(val, 'f', -1, 64), cmp, strconv.FormatFloat(thr, 'f', -1, 64)), When: time.Now(), Link: s.publicLink()}
		s.sendToChannels(ctx, tenant, m, event == "resolved")
	}()
}

func (s *Server) sendToChannels(ctx context.Context, tenant int64, m notify.Message, resolved bool) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, kind, config, min_severity, on_resolve FROM notification_channels WHERE tenant_id = $1 AND enabled`, tenant)
	if err != nil {
		return
	}
	type ch struct {
		id        int64
		kind, min string
		cfg       notify.Config
		onResolve bool
	}
	var list []ch
	for rows.Next() {
		var c ch
		var raw []byte
		if rows.Scan(&c.id, &c.kind, &raw, &c.min, &c.onResolve) == nil && json.Unmarshal(raw, &c.cfg) == nil {
			list = append(list, c)
		}
	}
	rows.Close()
	for _, c := range list {
		if sevRank[m.Severity] < sevRank[c.min] || (resolved && !c.onResolve) {
			continue
		}
		s.sendOne(ctx, c.id, c.kind, c.cfg, m)
	}
}

func (s *Server) sendOne(ctx context.Context, id int64, kind string, cfg notify.Config, m notify.Message) error {
	err := notify.Send(ctx, kind, cfg, m, notify.Env{Block: s.BlockPrivate, Guard: probeGuard(s.BlockPrivate)})
	msg := ""
	if err != nil {
		msg = err.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		log.Printf("bildirim kanalı #%d (%s) başarısız: %v", id, kind, err)
	}
	if err == nil {
		s.DB.ExecContext(ctx, `UPDATE notification_channels SET last_error = '', last_sent = now() WHERE id = $1`, id)
	} else {
		s.DB.ExecContext(ctx, `UPDATE notification_channels SET last_error = $2 WHERE id = $1`, id, msg)
	}
	return err
}

var _ = pq.Array

func probeGuard(block bool) func(network, address string, c syscall.RawConn) error {
	return probe.Guard(block)
}
