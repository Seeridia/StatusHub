BEGIN;

INSERT INTO vendors (id, slug, name, canonical_domain, aliases) VALUES
    ('10000000-0000-0000-0000-000000000021', 'instatus-demo', 'Instatus Demo', 'status.instatus.com', '{}'),
    ('10000000-0000-0000-0000-000000000022', 'betterstack-demo', 'Better Stack Status', 'status.betterstack.com', '{}'),
    ('10000000-0000-0000-0000-000000000023', 'statusio-demo', 'Status.io Status', 'api.status.io', '{}'),
    ('10000000-0000-0000-0000-000000000024', 'cachet-demo', 'Cachet Demo', 'demo.cachethq.io', '{}'),
    ('10000000-0000-0000-0000-000000000025', 'gatus-demo', 'Gatus Demo', 'status.twin.sh', '{}'),
    ('10000000-0000-0000-0000-000000000026', 'cstate-demo', 'cState Demo', 'cstate.mnts.lt', '{}')
ON CONFLICT (slug) DO UPDATE SET
    name=EXCLUDED.name, canonical_domain=EXCLUDED.canonical_domain,
    aliases=EXCLUDED.aliases, updated_at=statement_timestamp();

INSERT INTO sources (
    id, vendor_id, requested_url, canonical_url, source_type,
    adapter_name, adapter_version, next_poll_at
)
SELECT source_id::uuid, vendor.id, requested_url, canonical_url, source_type, adapter_name, adapter_version, statement_timestamp()
FROM (VALUES
    ('20000000-0000-0000-0000-000000000021', 'instatus-demo', 'https://status.instatus.com', 'https://status.instatus.com', 'status_page', 'instatus', '1'),
    ('20000000-0000-0000-0000-000000000022', 'betterstack-demo', 'https://status.betterstack.com', 'https://status.betterstack.com', 'status_page', 'betterstack', '1'),
    ('20000000-0000-0000-0000-000000000023', 'statusio-demo', 'https://api.status.io/1.0/status/51f6f2088643809b7200000d', 'https://api.status.io/1.0/status/51f6f2088643809b7200000d', 'status_page', 'status-io', '1'),
    ('20000000-0000-0000-0000-000000000024', 'cachet-demo', 'https://demo.cachethq.io', 'https://demo.cachethq.io', 'status_page', 'cachet', '1'),
    ('20000000-0000-0000-0000-000000000025', 'gatus-demo', 'https://status.twin.sh', 'https://status.twin.sh', 'synthetic', 'gatus', '1'),
    ('20000000-0000-0000-0000-000000000026', 'cstate-demo', 'https://cstate.mnts.lt', 'https://cstate.mnts.lt', 'status_page', 'cstate', '1')
) AS profile(source_id, vendor_slug, requested_url, canonical_url, source_type, adapter_name, adapter_version)
JOIN vendors vendor ON vendor.slug=profile.vendor_slug
ON CONFLICT DO NOTHING;

COMMIT;
