INSERT INTO vendors (id, slug, name, canonical_domain)
VALUES (
    '11111111-1111-4111-8111-111111111111',
    'github',
    'GitHub',
    'githubstatus.com'
)
ON CONFLICT (slug) DO UPDATE
SET name = EXCLUDED.name,
    canonical_domain = EXCLUDED.canonical_domain,
    updated_at = now();

INSERT INTO sources (
    id,
    vendor_id,
    requested_url,
    canonical_url,
    source_type,
    adapter_name,
    adapter_version,
    enabled,
    next_poll_at
)
VALUES (
    '22222222-2222-4222-8222-222222222222',
    (SELECT id FROM vendors WHERE slug = 'github'),
    'https://www.githubstatus.com',
    'https://www.githubstatus.com',
    'status_page',
    'atlassian-statuspage',
    'statuspage-v2/1',
    true,
    now()
)
ON CONFLICT (canonical_url) WHERE tenant_id IS NULL
DO UPDATE SET
    enabled = true,
    next_poll_at = now(),
    updated_at = now();
