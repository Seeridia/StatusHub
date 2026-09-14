// Address suggestions, not a guarantee of live availability. Always probe before saving.
// Structured endpoints checked 2026-09-14; AWS uses the dedicated public-health adapter.
export interface ServiceSuggestion {
  name: string;
  url: string;
  aliases?: string[];
}

export const serviceCatalog: ServiceSuggestion[] = [
  { name: "1Password", url: "https://status.1password.com/" },
  { name: "Airtable", url: "https://status.airtable.com/" },
  { name: "Akamai", url: "https://status.akamai.com/" },
  { name: "Anthropic", url: "https://status.claude.com/", aliases: ["Claude"] },
  { name: "Asana", url: "https://status.asana.com/" },
  { name: "Atlassian", url: "https://status.atlassian.com/" },
  {
    name: "AWS",
    url: "https://health.aws.amazon.com/",
    aliases: ["Amazon Web Services"],
  },
  { name: "Better Stack", url: "https://status.betterstack.com/" },
  { name: "Bitbucket", url: "https://bitbucket.status.atlassian.com/" },
  { name: "Box", url: "https://status.box.com/" },
  { name: "Brevo", url: "https://status.brevo.com/" },
  { name: "Canva", url: "https://www.canvastatus.com/" },
  { name: "CircleCI", url: "https://status.circleci.com/" },
  { name: "Clerk", url: "https://status.clerk.com/" },
  { name: "Cloudflare", url: "https://www.cloudflarestatus.com/" },
  { name: "Cloudinary", url: "https://status.cloudinary.com/" },
  { name: "Coinbase", url: "https://status.coinbase.com/" },
  { name: "Confluence", url: "https://confluence.status.atlassian.com/" },
  { name: "Coursera", url: "https://status.coursera.org/" },
  { name: "Datadog", url: "https://status.datadoghq.com/" },
  { name: "DigitalOcean", url: "https://status.digitalocean.com/" },
  { name: "Discord", url: "https://discordstatus.com/" },
  { name: "Docker", url: "https://www.dockerstatus.com/" },
  { name: "Dropbox", url: "https://status.dropbox.com/" },
  { name: "Elastic", url: "https://status.elastic.co/" },
  { name: "Figma", url: "https://status.figma.com/" },
  { name: "GitHub", url: "https://www.githubstatus.com/" },
  { name: "Grafana Cloud", url: "https://status.grafana.com/" },
  { name: "Grammarly", url: "https://status.grammarly.com/" },
  { name: "HashiCorp", url: "https://status.hashicorp.com/" },
  { name: "Honeycomb", url: "https://status.honeycomb.io/" },
  { name: "HubSpot", url: "https://status.hubspot.com/" },
  { name: "Jira", url: "https://jira-software.status.atlassian.com/" },
  { name: "Klaviyo", url: "https://status.klaviyo.com/" },
  { name: "LaunchDarkly", url: "https://status.launchdarkly.com/" },
  { name: "Linear", url: "https://linearstatus.com/" },
  { name: "Loom", url: "https://status.loom.com/" },
  { name: "Mailgun", url: "https://status.mailgun.com/" },
  { name: "Miro", url: "https://status.miro.com/" },
  { name: "Mixpanel", url: "https://status.mixpanel.com/" },
  {
    name: "monday.com",
    url: "https://status.monday.com/",
    aliases: ["Monday"],
  },
  { name: "MongoDB", url: "https://status.mongodb.com/" },
  { name: "Netlify", url: "https://www.netlifystatus.com/" },
  { name: "New Relic", url: "https://status.newrelic.com/" },
  { name: "npm", url: "https://status.npmjs.org/", aliases: ["npmjs"] },
  { name: "OpenAI", url: "https://status.openai.com/", aliases: ["ChatGPT"] },
  { name: "Plaid", url: "https://status.plaid.com/" },
  { name: "Postman", url: "https://status.postman.com/" },
  { name: "Reddit", url: "https://www.redditstatus.com/" },
  { name: "Render", url: "https://status.render.com/" },
  { name: "Retool", url: "https://status.retool.com/" },
  { name: "Segment", url: "https://status.segment.com/" },
  { name: "SendGrid", url: "https://status.sendgrid.com/" },
  { name: "Sentry", url: "https://status.sentry.io/" },
  { name: "Shopify", url: "https://www.shopifystatus.com/" },
  { name: "Snowflake", url: "https://status.snowflake.com/" },
  { name: "Squarespace", url: "https://status.squarespace.com/" },
  { name: "Supabase", url: "https://status.supabase.com/" },
  { name: "Tailscale", url: "https://status.tailscale.com/" },
  { name: "Trello", url: "https://trello.status.atlassian.com/" },
  { name: "Twilio", url: "https://status.twilio.com/" },
  { name: "Typeform", url: "https://status.typeform.com/" },
  { name: "Vercel", url: "https://www.vercel-status.com/" },
  { name: "Webflow", url: "https://status.webflow.com/" },
  { name: "WorkOS", url: "https://status.workos.com/" },
  { name: "Zapier", url: "https://status.zapier.com/" },
  { name: "Zoom", url: "https://status.zoom.us/" },
];

export function filterServices(query: string): ServiceSuggestion[] {
  const needle = query.trim().toLowerCase();
  return serviceCatalog.filter((service) =>
    [service.name, service.url, ...(service.aliases || [])].some((value) =>
      value.toLowerCase().includes(needle),
    ),
  );
}
