-- Yeni probe türleri: snmpx (gelişmiş SNMP) ve trace (yol izleme). Yalnızca kısıt genişler; veri değişmez.
ALTER TABLE probes DROP CONSTRAINT IF EXISTS probes_type_check;
ALTER TABLE probes ADD CONSTRAINT probes_type_check CHECK (type IN ('http', 'tcp', 'postgres', 'icmp', 'snmp', 'dns', 'tls', 'udp', 'ntp', 'host', 'snmpx', 'trace'));
