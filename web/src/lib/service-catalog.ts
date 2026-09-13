// Address suggestions, not a guarantee of live availability. Always probe before saving.
// Summary JSON checked 2026-09-13; AWS uses the existing aws-public-health adapter.
export interface ServiceSuggestion {
  name: string;
  url: string;
  aliases?: string[];
}
export const serviceCatalog: ServiceSuggestion[] = [
  {
    name: "Airtable",
    url: "https://status.airtable.com/",
  },
  {
    name: "Anthropic",
    url: "https://status.claude.com/",
    aliases: ["Claude"],
  },
  {
    name: "Asana",
    url: "https://status.asana.com/",
  },
  {
    name: "AWS",
    url: "https://health.aws.amazon.com/",
    aliases: ["Amazon Web Services"],
  },
  {
    name: "Box",
    url: "https://status.box.com/",
  },
  {
    name: "Canva",
    url: "https://www.canvastatus.com/",
  },
  {
    name: "CircleCI",
    url: "https://status.circleci.com/",
  },
  {
    name: "Cloudflare",
    url: "https://www.cloudflarestatus.com/",
  },
  {
    name: "Confluence",
    url: "https://confluence.status.atlassian.com/",
  },
  {
    name: "Datadog",
    url: "https://status.datadoghq.com/",
  },
  {
    name: "DigitalOcean",
    url: "https://status.digitalocean.com/",
  },
  {
    name: "Discord",
    url: "https://discordstatus.com/",
  },
  {
    name: "Docker",
    url: "https://www.dockerstatus.com/",
  },
  {
    name: "Dropbox",
    url: "https://status.dropbox.com/",
  },
  {
    name: "Figma",
    url: "https://status.figma.com/",
  },
  {
    name: "GitHub",
    url: "https://www.githubstatus.com/",
  },
  {
    name: "Grafana Cloud",
    url: "https://status.grafana.com/",
  },
  {
    name: "HubSpot",
    url: "https://status.hubspot.com/",
  },
  {
    name: "Jira",
    url: "https://jira-software.status.atlassian.com/",
  },
  {
    name: "Linear",
    url: "https://linearstatus.com/",
  },
  {
    name: "Mailgun",
    url: "https://status.mailgun.com/",
  },
  {
    name: "Miro",
    url: "https://status.miro.com/",
  },
  {
    name: "MongoDB",
    url: "https://status.mongodb.com/",
  },
  {
    name: "Netlify",
    url: "https://www.netlifystatus.com/",
  },
  {
    name: "New Relic",
    url: "https://status.newrelic.com/",
  },
  {
    name: "OpenAI",
    url: "https://status.openai.com/",
  },
  {
    name: "Reddit",
    url: "https://www.redditstatus.com/",
  },
  {
    name: "Render",
    url: "https://status.render.com/",
  },
  {
    name: "SendGrid",
    url: "https://status.sendgrid.com/",
  },
  {
    name: "Sentry",
    url: "https://status.sentry.io/",
  },
  {
    name: "Supabase",
    url: "https://status.supabase.com/",
  },
  {
    name: "Trello",
    url: "https://trello.status.atlassian.com/",
  },
  {
    name: "Twilio",
    url: "https://status.twilio.com/",
  },
  {
    name: "Vercel",
    url: "https://www.vercel-status.com/",
  },
  {
    name: "Webflow",
    url: "https://status.webflow.com/",
  },
  {
    name: "Zoom",
    url: "https://status.zoom.us/",
  },
];
export function filterServices(query: string): ServiceSuggestion[] {
  const needle = query.trim().toLowerCase();
  return serviceCatalog.filter((service) =>
    [service.name, service.url, ...(service.aliases || [])].some((value) =>
      value.toLowerCase().includes(needle),
    ),
  );
}
