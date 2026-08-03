export type LinkPreviewFallbackType =
  | "calendar"
  | "bugtracker"
  | "shopping"
  | "code"
  | "cloud"
  | "video"
  | "map"
  | "spreadsheet"
  | "presentation"
  | "document"
  | "link";

export type LinkPreviewFallbackBrand =
  | "amazon"
  | "youtrack"
  | "jira"
  | "github"
  | "google-calendar"
  | "sharepoint"
  | "youtube";

export type LinkPreviewFallback = {
  type: LinkPreviewFallbackType;
  brand?: LinkPreviewFallbackBrand;
};

type FallbackRule = {
  type: Exclude<LinkPreviewFallbackType, "link">;
  domains: string[];
};

// Keep domain-specific fallbacks here so the catalogue can grow without
// adding conditions to the preview component. Subdomains match automatically.
const FALLBACK_RULES: FallbackRule[] = [
  {
    type: "calendar",
    domains: ["calendar.google.com", "calendar.google.fr", "outlook.live.com", "outlook.office.com", "cal.com"],
  },
  {
    type: "bugtracker",
    domains: ["youtrack.cloud", "myjetbrains.com", "atlassian.net", "jira.com", "linear.app", "shortcut.com"],
  },
  {
    type: "shopping",
    domains: ["amazon.fr", "amazon.com", "amzn.eu", "amzn.to", "but.fr", "ebay.fr", "ebay.com", "etsy.com", "fnac.com", "cdiscount.com", "leboncoin.fr"],
  },
  {
    type: "code",
    domains: ["github.com", "gitlab.com", "bitbucket.org", "codeberg.org"],
  },
  {
    type: "cloud",
    domains: ["drive.google.com", "dropbox.com", "box.com", "onedrive.live.com", "1drv.ms", "sharepoint.com", "sharepointonline.com"],
  },
  {
    type: "video",
    domains: ["youtube.com", "youtu.be", "vimeo.com", "dailymotion.com", "twitch.tv"],
  },
  {
    type: "map",
    domains: ["maps.google.com", "maps.google.fr", "openstreetmap.org", "maps.apple.com", "waze.com"],
  },
  {
    type: "document",
    domains: ["docs.google.com", "notion.so", "notion.site", "confluence.com", "wikipedia.org"],
  },
];

function domainMatches(hostname: string, domain: string): boolean {
  return hostname === domain || hostname.endsWith(`.${domain}`);
}

export function getLinkPreviewFallback(rawURL: string): LinkPreviewFallback {
  try {
    const parsedURL = new URL(rawURL);
    const hostname = parsedURL.hostname.toLowerCase().replace(/^www\./, "");
    let linkContent = `${parsedURL.pathname}${parsedURL.search}`.toLowerCase();
    try {
      linkContent = decodeURIComponent(linkContent);
    } catch {
      // A malformed percent escape must not prevent the domain fallback.
    }

    // File types are more informative than their hosting domain. The boundary
    // also supports filenames carried in query parameters such as ?file=... .
    if (/\.(xlsx|xls|xlsm|xlsb|ods|csv|tsv)(?:$|[?&#/])/i.test(linkContent) || parsedURL.pathname.toLowerCase().startsWith("/:x:")) {
      return { type: "spreadsheet" };
    }
    if (/\.(pptx|ppt|ppsx|pps|odp|key)(?:$|[?&#/])/i.test(linkContent) || parsedURL.pathname.toLowerCase().startsWith("/:p:")) {
      return { type: "presentation" };
    }
    if (/\.(docx|doc|odt|rtf|txt|pdf|pages)(?:$|[?&#/])/i.test(linkContent) || parsedURL.pathname.toLowerCase().startsWith("/:w:")) {
      return { type: "document" };
    }

    if (domainMatches(hostname, "amazon.fr") || domainMatches(hostname, "amazon.com") || domainMatches(hostname, "amzn.eu") || domainMatches(hostname, "amzn.to")) {
      return { type: "shopping", brand: "amazon" };
    }
    if (domainMatches(hostname, "youtrack.cloud") || domainMatches(hostname, "myjetbrains.com") || hostname.split(".").includes("youtrack")) {
      return { type: "bugtracker", brand: "youtrack" };
    }
    if (domainMatches(hostname, "atlassian.net") || domainMatches(hostname, "jira.com")) {
      return { type: "bugtracker", brand: "jira" };
    }
    if (domainMatches(hostname, "github.com")) {
      return { type: "code", brand: "github" };
    }
    if (domainMatches(hostname, "calendar.google.com") || domainMatches(hostname, "calendar.google.fr")) {
      return { type: "calendar", brand: "google-calendar" };
    }
    if (domainMatches(hostname, "sharepoint.com") || domainMatches(hostname, "sharepointonline.com")) {
      return { type: "cloud", brand: "sharepoint" };
    }
    if (domainMatches(hostname, "youtube.com") || domainMatches(hostname, "youtu.be")) {
      return { type: "video", brand: "youtube" };
    }

    // Google Maps commonly uses a normal Google hostname and a /maps path.
    if ((domainMatches(hostname, "google.com") || domainMatches(hostname, "google.fr")) && parsedURL.pathname.startsWith("/maps")) {
      return { type: "map" };
    }

    return { type: FALLBACK_RULES.find((rule) => rule.domains.some((domain) => domainMatches(hostname, domain)))?.type ?? "link" };
  } catch {
    return { type: "link" };
  }
}
