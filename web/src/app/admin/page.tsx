import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { AdminApp } from "@/components/admin/AdminApp";

export const metadata: Metadata = {
  title: "Raincast – Admin",
  robots: { index: false, follow: false },
};

// The admin page is for `next dev` next to the backend and the collector's
// database; production builds (Vercel) have no admin API behind them.
export default function AdminPage() {
  if (process.env.NODE_ENV === "production") notFound();
  return <AdminApp />;
}
