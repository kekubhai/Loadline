import type { MetadataRoute } from "next";
import { absolute } from "./site";

/**
 * Sitemap — the indexable surface of the site: the application plus the
 * server-rendered content pages that carry SEO/AEO weight.
 */
export default function sitemap(): MetadataRoute.Sitemap {
  const lastModified = new Date();
  return [
    {
      url: absolute("/"),
      lastModified,
      changeFrequency: "weekly",
      priority: 1,
    },
    {
      url: absolute("/docs"),
      lastModified,
      changeFrequency: "monthly",
      priority: 0.8,
    },
    {
      url: absolute("/faq"),
      lastModified,
      changeFrequency: "monthly",
      priority: 0.7,
    },
  ];
}
