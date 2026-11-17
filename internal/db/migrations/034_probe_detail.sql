-- v2.7.1: probe ayrıntıları.
--  * probes.last_detail : son ölçümün türe özgü ayrıntıları ([{"k": "...", "v": "..."}]); gizli değer içermez
ALTER TABLE probes ADD COLUMN IF NOT EXISTS last_detail jsonb NOT NULL DEFAULT '[]'::jsonb;
