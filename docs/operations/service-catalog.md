# Service address suggestions / 服务地址目录

The Add source form uses TDesign SelectInput to search by service name, alias,
or status-page hostname. Selecting an entry fills its URL. Unlisted services
remain supported through manual URL entry and live adapter discovery. The name
is an optional display label, never an identity key.

添加数据源时可按服务名、别名或状态页域名搜索。选择目录项会自动填写地址；
未收录的服务仍可手动填写地址并由系统实时探测适配器。服务名称只是展示名称，
不作为服务身份标识。

The built-in directory currently contains **67 services**. Of these, 65 exposed
valid Atlassian Statuspage summary data in checks completed through 2026-09-14, Better Stack
was recognized by the existing Better Stack adapter, and AWS uses the dedicated
public-health adapter. A live probe is still mandatory before a source is saved,
because status pages and upstream availability can change.

目录当前覆盖 **67 个服务**：其中 65 个截至 2026-09-14 已验证 Statuspage 结构化
摘要，Better Stack 使用现有专用适配器，AWS 使用公共健康专用适配器。保存前仍会
实时探测，目录只负责地址补全，不代表上游未来始终可用。

The catalog includes common infrastructure, developer tooling, SaaS, commerce,
and communications services, including AWS, OpenAI, Anthropic, Cloudflare,
GitHub, Atlassian, Bitbucket, Clerk, Elastic, HashiCorp, LaunchDarkly, npm,
Postman, Shopify, Snowflake, Tailscale, Twilio, Vercel, WorkOS, and Zoom. The
canonical, alphabetized list and URLs live in
`web/src/lib/service-catalog.ts` so the UI has one source of truth.

## Maintaining the directory

1. Confirm the service's official status-page URL.
2. Run the StatusHub probe against that URL and require a recognized adapter.
3. Add the name, canonical URL, and useful aliases to
   `web/src/lib/service-catalog.ts`.
4. Run `npm --prefix web run build`, then manually verify search, selection, URL autofill, and an unlisted service using the live probe in an isolated environment.

Do not derive this list from a logo catalog. Logos identify brands; they do not
prove adapter compatibility.
