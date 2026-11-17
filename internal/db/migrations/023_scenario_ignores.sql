-- Tarayıcı senaryolarında sayfa değişikliği karşılaştırmasından hariç tutulan öğeler (adım başına).
-- kind='item': a ile birebir eşleşen öğe yok sayılır. kind='value': a ile başlayıp b ile biten öğenin arası '~' ile maskelenir.
CREATE TABLE IF NOT EXISTS scenario_ignores (
  id          bigserial PRIMARY KEY,
  scenario_id bigint NOT NULL REFERENCES scenarios(id) ON DELETE CASCADE,
  step_idx    integer NOT NULL,
  kind        text NOT NULL CHECK (kind IN ('item','value')),
  a           text NOT NULL,
  b           text NOT NULL DEFAULT '',
  created_by  text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (scenario_id, step_idx, kind, a, b)
);
