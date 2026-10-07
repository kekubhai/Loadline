import { readFileSync } from "node:fs";
import path from "node:path";
import { FAQ_ITEMS } from "../faq";
import { GITHUB_URL, SITE_DESCRIPTION, absolute } from "../site";

/**
 * llms-full.txt — the complete markdown context for language models:
 * the site summary, every FAQ answer, and the full simulation-model
 * specification read from docs/simulation-model.md at build time, so it
 * can never drift from the source document.
 */
export const dynamic = "force-static";

function loadSpec(): string {
  try {
    const file = path.join(process.cwd(), "..", "..", "docs", "simulation-model.md");
    return readFileSync(file, "utf8");
  } catch {
    // The spec lives in the repository regardless of deployment shape.
    return `The full simulation-model specification is maintained at:\n${GITHUB_URL}/blob/main/docs/simulation-model.md\n`;
  }
}

function build(): string {
  const faq = FAQ_ITEMS.map(
    (item) => `### ${item.question}\n\n${item.answer}`,
  ).join("\n\n");

  return `# LOADLINE — full reference

> ${SITE_DESCRIPTION}

LOADLINE answers: *Can this architecture actually handle my requirements, what will it cost, and where will it break?* Core loop: requirements → architecture → simulation → failure → diagnosis → modification → comparison.

- Workbench: ${absolute("/")}
- How it works: ${absolute("/docs")}
- FAQ: ${absolute("/faq")}
- Source: ${GITHUB_URL}
- License: MIT

## Frequently asked questions

${faq}

## Simulation model specification

${loadSpec()}
`;
}

export function GET(): Promise<Response> {
  return Promise.resolve(
    new Response(build(), {
      headers: { "Content-Type": "text/markdown; charset=utf-8" },
    }),
  );
}
