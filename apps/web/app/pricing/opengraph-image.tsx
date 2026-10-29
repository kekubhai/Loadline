import { OG_SIZE, ogCard } from "../ogcard";

export const runtime = "nodejs";

export const alt = "LOADLINE pricing — Free, Pro, Team and Enterprise plans";
export const size = OG_SIZE;
export const contentType = "image/png";

export default function OpengraphImage() {
  return ogCard({
    eyebrow: "pricing",
    title: "Free to start. Pro, Team & Enterprise when you scale.",
    subtitle: "Free $0 · Pro $9 · Team $29 · Enterprise — 20% off billed annually.",
    chips: ["free tier", "pro", "team", "enterprise"],
    path: "/pricing",
  });
}
