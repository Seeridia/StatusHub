# Service address suggestions / 服务地址目录

The Add source form uses TDesign SelectInput to search by service name, alias or status-page hostname. Selecting an entry fills its URL; unlisted services remain supported through manual URL entry and live adapter discovery. The name is an optional display label for a new vendor, never an identity key or a rename operation for existing vendors.

添加来源时可搜索名称、别名或域名。选择目录项自动填写地址；自定义服务手动填写地址后实时检测。目录不是完整支持名单，也不保证上游持续可用。

These 35 Statuspage-compatible summary endpoints returned structured page and component data on 2026-09-13. AWS uses the existing dedicated public-health adapter. A live probe remains mandatory before connecting; endpoint availability is not an end-to-end collection guarantee.

| Service | Status page |
| --- | --- |
| Airtable | https://status.airtable.com/ |
| Anthropic | https://status.claude.com/ |
| Asana | https://status.asana.com/ |
| AWS | https://health.aws.amazon.com/ |
| Box | https://status.box.com/ |
| Canva | https://www.canvastatus.com/ |
| CircleCI | https://status.circleci.com/ |
| Cloudflare | https://www.cloudflarestatus.com/ |
| Confluence | https://confluence.status.atlassian.com/ |
| Datadog | https://status.datadoghq.com/ |
| DigitalOcean | https://status.digitalocean.com/ |
| Discord | https://discordstatus.com/ |
| Docker | https://www.dockerstatus.com/ |
| Dropbox | https://status.dropbox.com/ |
| Figma | https://status.figma.com/ |
| GitHub | https://www.githubstatus.com/ |
| Grafana Cloud | https://status.grafana.com/ |
| HubSpot | https://status.hubspot.com/ |
| Jira | https://jira-software.status.atlassian.com/ |
| Linear | https://linearstatus.com/ |
| Mailgun | https://status.mailgun.com/ |
| Miro | https://status.miro.com/ |
| MongoDB | https://status.mongodb.com/ |
| Netlify | https://www.netlifystatus.com/ |
| New Relic | https://status.newrelic.com/ |
| OpenAI | https://status.openai.com/ |
| Reddit | https://www.redditstatus.com/ |
| Render | https://status.render.com/ |
| SendGrid | https://status.sendgrid.com/ |
| Sentry | https://status.sentry.io/ |
| Supabase | https://status.supabase.com/ |
| Trello | https://trello.status.atlassian.com/ |
| Twilio | https://status.twilio.com/ |
| Vercel | https://www.vercel-status.com/ |
| Webflow | https://status.webflow.com/ |
| Zoom | https://status.zoom.us/ |

## Maintaining the directory

Edit `web/src/lib/service-catalog.ts`. Verify the official URL and its structured endpoints against an existing adapter before adding a suggestion. Add useful aliases where appropriate (for example, Claude → Anthropic). Run `npm --prefix web test` and `npm --prefix web run build`. Do not derive this list from landing-page logos: those illustrate the ecosystem and are not a verified support registry.
