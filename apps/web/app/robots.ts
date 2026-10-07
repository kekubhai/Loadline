import type { MetadataRoute } from "next";
import { SITE_URL } from "./site";

/**
 * robots.txt — everything crawlable, with AI/answer-engine crawlers
 * explicitly allowed (generative engine optimization: GPTBot, ClaudeBot,
 * PerplexityBot et al. should be able to read and cite the site).
 */
const AI_AGENTS = [
  "GPTBot",
  "OAI-SearchBot",
  "ChatGPT-User",
  "ClaudeBot",
  "Claude-Web",
  "anthropic-ai",
  "PerplexityBot",
  "Google-Extended",
  "CCBot",
  "Bytespider",
  "cohere-ai",
  "Diffbot",
  "meta-externalagent",
  "Amazonbot",
];

export default function robots(): MetadataRoute.Robots {
  return {
    rules: [
      ...AI_AGENTS.map((userAgent) => ({ userAgent, allow: "/" })),
      { userAgent: "*", allow: "/" },
    ],
    // No `host` directive: it expects a bare host, and SITE_URL is an
    // origin with a scheme — emitting a malformed value would be worse.
    sitemap: `${SITE_URL}/sitemap.xml`,
  };
}
