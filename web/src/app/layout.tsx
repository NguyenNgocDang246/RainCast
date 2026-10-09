import type { Metadata, Viewport } from "next";
import { Be_Vietnam_Pro } from "next/font/google";
import { Analytics } from "@vercel/analytics/next";
import { en } from "@/lib/messages/en";
import "./globals.css";

const sans = Be_Vietnam_Pro({
  variable: "--font-sans-vi",
  subsets: ["latin", "vietnamese"],
  weight: ["400", "500", "600"],
});

export const metadata: Metadata = {
  title: en.meta.title,
  description: en.meta.description,
};

// The page has its own dark palette; light keeps browsers (auto dark mode,
// default form controls) from recoloring it.
export const viewport: Viewport = {
  colorScheme: "light",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={`${sans.variable} h-full antialiased`}>
      <body className="min-h-full flex flex-col">{children}
        <Analytics />
      </body>
    </html>
  );
}
