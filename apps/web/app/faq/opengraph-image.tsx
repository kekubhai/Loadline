import { OG_SIZE, ogCard } from "../ogcard";

export const runtime = "nodejs";

export const alt = "LOADLINE FAQ — system design simulation, capacity, latency and cost";
export const size = OG_SIZE;
export const contentType = "image/png";

export default function OpengraphImage() {
  return ogCard({
    eyebrow: "faq",
    title: "What the simulator measures — and what it doesn't promise",
    subtitle: "p99 and capacity, determinism, providers, failure injection and sharing.",
    chips: ["deterministic seeds", "Cloudflare · AWS · GCP", "failure injection", "estimates, not billing"],
    path: "/faq",
  });
}
