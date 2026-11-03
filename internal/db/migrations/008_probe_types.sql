-- Yeni probe türleri (icmp, snmp, dns, tls, udp, ntp) ve türe özgü seçenekler (SNMP topluluk adı gizli değerdir).
ALTER TABLE probes DROP CONSTRAINT IF EXISTS probes_type_check;
ALTER TABLE probes ADD CONSTRAINT probes_type_check CHECK (type IN ('http', 'tcp', 'postgres', 'icmp', 'snmp', 'dns', 'tls', 'udp', 'ntp'));
ALTER TABLE probes ADD COLUMN options jsonb NOT NULL DEFAULT '{}';
