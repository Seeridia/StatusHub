// Brand identifiers only. Artwork is loaded from third-party CDNs at runtime.
export const ecosystemBrands = [
  { slug: "1password", name: "1Password" },
  {
    slug: "airtable",
    name: "Airtable",
  },
  {
    slug: "amazonwebservices",
    name: "AWS",
  },
  { slug: "akamai", name: "Akamai" },
  {
    slug: "cloudflare",
    name: "Cloudflare",
  },
  {
    slug: "github",
    name: "GitHub",
  },
  {
    slug: "openai",
    name: "OpenAI",
  },
  {
    slug: "anthropic",
    name: "Anthropic",
  },
  {
    slug: "googlecloud",
    name: "Google Cloud",
  },
  {
    slug: "digitalocean",
    name: "DigitalOcean",
  },
  {
    slug: "vercel",
    name: "Vercel",
  },
  {
    slug: "netlify",
    name: "Netlify",
  },
  {
    slug: "render",
    name: "Render",
  },
  {
    slug: "flydotio",
    name: "Fly.io",
  },
  {
    slug: "railway",
    name: "Railway",
  },
  {
    slug: "supabase",
    name: "Supabase",
  },
  {
    slug: "firebase",
    name: "Firebase",
  },
  {
    slug: "mongodb",
    name: "MongoDB",
  },
  {
    slug: "planetscale",
    name: "PlanetScale",
  },
  {
    slug: "neon",
    name: "Neon",
  },
  {
    slug: "redis",
    name: "Redis",
  },
  {
    slug: "elastic",
    name: "Elastic",
  },
  {
    slug: "snowflake",
    name: "Snowflake",
  },
  {
    slug: "databricks",
    name: "Databricks",
  },
  {
    slug: "upstash",
    name: "Upstash",
  },
  {
    slug: "stripe",
    name: "Stripe",
  },
  {
    slug: "paypal",
    name: "PayPal",
  },
  {
    slug: "adyen",
    name: "Adyen",
  },
  {
    slug: "twilio",
    name: "Twilio",
  },
  {
    slug: "mailgun",
    name: "Mailgun",
  },
  {
    slug: "resend",
    name: "Resend",
  },
  {
    slug: "slack",
    name: "Slack",
  },
  {
    slug: "discord",
    name: "Discord",
  },
  {
    slug: "zoom",
    name: "Zoom",
  },
  {
    slug: "notion",
    name: "Notion",
  },
  {
    slug: "linear",
    name: "Linear",
  },
  {
    slug: "atlassian",
    name: "Atlassian",
  },
  { slug: "bitbucket", name: "Bitbucket" },
  { slug: "brevo", name: "Brevo" },
  {
    slug: "jira",
    name: "Jira",
  },
  {
    slug: "confluence",
    name: "Confluence",
  },
  {
    slug: "trello",
    name: "Trello",
  },
  {
    slug: "asana",
    name: "Asana",
  },
  {
    slug: "figma",
    name: "Figma",
  },
  {
    slug: "miro",
    name: "Miro",
  },
  {
    slug: "dropbox",
    name: "Dropbox",
  },
  {
    slug: "box",
    name: "Box",
  },
  {
    slug: "hubspot",
    name: "HubSpot",
  },
  { slug: "clerk", name: "Clerk" },
  { slug: "cloudinary", name: "Cloudinary" },
  { slug: "coinbase", name: "Coinbase" },
  { slug: "coursera", name: "Coursera" },
  {
    slug: "zendesk",
    name: "Zendesk",
  },
  {
    slug: "intercom",
    name: "Intercom",
  },
  {
    slug: "shopify",
    name: "Shopify",
  },
  {
    slug: "webflow",
    name: "Webflow",
  },
  {
    slug: "sentry",
    name: "Sentry",
  },
  {
    slug: "datadog",
    name: "Datadog",
  },
  {
    slug: "grafana",
    name: "Grafana",
  },
  {
    slug: "newrelic",
    name: "New Relic",
  },
  {
    slug: "pagerduty",
    name: "PagerDuty",
  },
  {
    slug: "betterstack",
    name: "Better Stack",
  },
  {
    slug: "circleci",
    name: "CircleCI",
  },
  {
    slug: "gitlab",
    name: "GitLab",
  },
  {
    slug: "docker",
    name: "Docker",
  },
  {
    slug: "npm",
    name: "npm",
  },
  {
    slug: "huggingface",
    name: "Hugging Face",
  },
  {
    slug: "auth0",
    name: "Auth0",
  },
  {
    slug: "fastly",
    name: "Fastly",
  },
  { slug: "grammarly", name: "Grammarly" },
  { slug: "hashicorp", name: "HashiCorp" },
  { slug: "honeycomb", name: "Honeycomb" },
  { slug: "klaviyo", name: "Klaviyo" },
  { slug: "launchdarkly", name: "LaunchDarkly" },
  { slug: "loom", name: "Loom" },
  { slug: "mixpanel", name: "Mixpanel" },
  { slug: "mondaydotcom", name: "monday.com" },
  { slug: "plaid", name: "Plaid" },
  { slug: "postman", name: "Postman" },
  { slug: "retool", name: "Retool" },
  { slug: "segment", name: "Segment" },
  { slug: "squarespace", name: "Squarespace" },
  { slug: "tailscale", name: "Tailscale" },
  { slug: "typeform", name: "Typeform" },
  { slug: "workos", name: "WorkOS" },
  { slug: "zapier", name: "Zapier" },
  {
    slug: "canva",
    name: "Canva",
  },
  {
    slug: "reddit",
    name: "Reddit",
  },
  {
    slug: "sendgrid",
    name: "SendGrid",
  },
] as const;

export type EcosystemBrand = (typeof ecosystemBrands)[number];

const DEVICON_BASE =
  "https://cdn.jsdelivr.net/gh/devicons/devicon@latest/icons";
const SIMPLE_ICONS_BASE = "https://cdn.simpleicons.org";

const deviconByBrand: Record<string, readonly [string, string]> = {
  AWS: ["amazonwebservices", "original-wordmark"],
  Bitbucket: ["bitbucket", "original"],
  Canva: ["canva", "original"],
  CircleCI: ["circleci", "plain"],
  Cloudflare: ["cloudflare", "original"],
  Confluence: ["confluence", "original"],
  Datadog: ["datadog", "original"],
  DigitalOcean: ["digitalocean", "original"],
  Docker: ["docker", "original"],
  Figma: ["figma", "original"],
  GitHub: ["github", "original"],
  Grafana: ["grafana", "original"],
  Jira: ["jira", "original"],
  MongoDB: ["mongodb", "original"],
  Netlify: ["netlify", "original"],
  "New Relic": ["newrelic", "original"],
  npm: ["npm", "original-wordmark"],
  Postman: ["postman", "original"],
  Sentry: ["sentry", "original"],
  Supabase: ["supabase", "original"],
  Trello: ["trello", "original"],
  Twilio: ["twilio", "original"],
  Vercel: ["vercel", "original"],
  Webflow: ["webflow", "original"],
};

const brandAliases: Record<string, string> = {
  "amazon web services": "AWS",
  "grafana cloud": "Grafana",
};

export function findEcosystemBrand(name: string): EcosystemBrand | undefined {
  const normalized = name.trim().toLowerCase();
  const canonical = brandAliases[normalized]?.toLowerCase() || normalized;
  return ecosystemBrands.find(
    (brand) => brand.name.toLowerCase() === canonical,
  );
}

export function brandIconSources(brand: EcosystemBrand): string[] {
  const devicon = deviconByBrand[brand.name];
  if (devicon) {
    const [slug, variant] = devicon;
    return [
      `${DEVICON_BASE}/${slug}/${slug}-${variant}.svg`,
      `${SIMPLE_ICONS_BASE}/${brand.slug}?viewbox=auto`,
    ];
  }
  return [
    `${DEVICON_BASE}/${brand.slug}/${brand.slug}-original.svg`,
    `${DEVICON_BASE}/${brand.slug}/${brand.slug}-plain.svg`,
    `${SIMPLE_ICONS_BASE}/${brand.slug}?viewbox=auto`,
  ];
}

export function brandIconSourcesForName(name: string): string[] {
  const known = findEcosystemBrand(name);
  if (known) return brandIconSources(known);
  const slug = name
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/&/g, "and")
    .replace(/[^a-z0-9]+/g, "")
    .slice(0, 64);
  if (!slug) return [];
  return [
    `${DEVICON_BASE}/${slug}/${slug}-original.svg`,
    `${DEVICON_BASE}/${slug}/${slug}-plain.svg`,
    `${SIMPLE_ICONS_BASE}/${slug}?viewbox=auto`,
  ];
}
