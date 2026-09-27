import type { Metadata } from "next";
import type { ReactNode } from "react";
import "./globals.css";
import "./primitives.css";
import "./shell.css";
import "./analysis.css";

export const metadata: Metadata = {
  title: "LOADLINE",
  description:
    "System-design simulation: requirements → architecture → simulation → failure → diagnosis.",
};

export default function RootLayout({ children }: Readonly<{ children: ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
