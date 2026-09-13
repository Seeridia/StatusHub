BEGIN;

INSERT INTO vendors (id, slug, name, canonical_domain, aliases) VALUES
    ('10000000-0000-0000-0000-000000000011', 'github', 'GitHub', 'www.githubstatus.com', ARRAY['githubstatus.com']),
    ('10000000-0000-0000-0000-000000000012', 'cloudflare', 'Cloudflare', 'www.cloudflarestatus.com', ARRAY['cloudflarestatus.com']),
    ('10000000-0000-0000-0000-000000000013', 'openai', 'OpenAI', 'status.openai.com', '{}'),
    ('10000000-0000-0000-0000-000000000014', 'anthropic', 'Anthropic / Claude', 'status.claude.com', ARRAY['status.anthropic.com']),
    ('10000000-0000-0000-0000-000000000015', 'aws', 'Amazon Web Services', 'health.aws.amazon.com', ARRAY['status.aws.amazon.com'])
ON CONFLICT (slug) DO UPDATE SET
    name=EXCLUDED.name, canonical_domain=EXCLUDED.canonical_domain,
    aliases=EXCLUDED.aliases, updated_at=statement_timestamp();

INSERT INTO sources (
    id, vendor_id, requested_url, canonical_url, source_type,
    adapter_name, adapter_version, next_poll_at
)
SELECT source_id::uuid, vendor.id, requested_url, canonical_url, source_type, adapter_name, adapter_version, statement_timestamp()
FROM (VALUES
    ('20000000-0000-0000-0000-000000000011', 'github', 'https://www.githubstatus.com', 'https://www.githubstatus.com', 'status_page', 'vendor-profile', '1'),
    ('20000000-0000-0000-0000-000000000012', 'cloudflare', 'https://www.cloudflarestatus.com', 'https://www.cloudflarestatus.com', 'status_page', 'vendor-profile', '1'),
    ('20000000-0000-0000-0000-000000000013', 'openai', 'https://status.openai.com', 'https://status.openai.com', 'status_page', 'vendor-profile', '1'),
    ('20000000-0000-0000-0000-000000000014', 'anthropic', 'https://status.anthropic.com', 'https://status.claude.com', 'status_page', 'vendor-profile', '1'),
    ('20000000-0000-0000-0000-000000000015', 'aws', 'https://health.aws.amazon.com/health/status', 'https://health.aws.amazon.com/health/status', 'feed', 'vendor-profile', '1')
) AS profile(source_id, vendor_slug, requested_url, canonical_url, source_type, adapter_name, adapter_version)
JOIN vendors vendor ON vendor.slug=profile.vendor_slug
ON CONFLICT DO NOTHING;

COMMIT;
