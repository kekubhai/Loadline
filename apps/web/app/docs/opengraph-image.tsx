import { OG_SIZE, ogCard } from "../ogcard";

export const runtime = "nodejs";

export const alt = "How LOADLINE models capacity, latency, failure and cost";
export const size = OG_SIZE;
export const contentType = "image/png";

export default function OpengraphImage() {
  return ogCard({
    eyebrow: "docs · simulation model",
    title: "How LOADLINE models capacity, latency, failure & cost",
    subtitle: "Every formula and assumption behind the numbers, inspectable.",
    chips: ["users to peak RPS","queue & service time", "measured p99", "capacity ceilings", "cost assumptions"],
    path: "/docs",
  });
}
