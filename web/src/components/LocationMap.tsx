"use client";

import "leaflet/dist/leaflet.css";
import type * as Leaflet from "leaflet";
import { useEffect, useRef, useState } from "react";
import { API_BASE, type Place } from "@/lib/api";
import { useLocale } from "@/lib/i18n";

type Props = {
  /** The chosen place, shown as a pin; the map flies to it when chosen elsewhere. */
  place: Place | null;
  /** A point clicked on the map or where the pin was dropped. */
  onPick: (lat: number, lon: number) => void;
};

/** Ho Chi Minh City, the radar's home area. */
const HOME: [number, number] = [10.78, 106.7];
const HOME_ZOOM = 10;
const PLACE_ZOOM = 14;
const MAX_ZOOM = 18; // geocode.MaxTileZoom

const ATTRIBUTION =
  'Powered by <a href="https://www.geoapify.com/" target="_blank" rel="noreferrer">Geoapify</a> · ' +
  '© <a href="https://openmaptiles.org/" target="_blank" rel="noreferrer">OpenMapTiles</a> ' +
  '© <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer">OpenStreetMap</a> contributors';

/** Same point within ~1 m. */
const near = (a: { lat: number; lon: number }, b: { lat: number; lon: number }) =>
  Math.abs(a.lat - b.lat) < 1e-5 && Math.abs(a.lon - b.lon) < 1e-5;

export function LocationMap({ place, onPick }: Props) {
  const { t } = useLocale();
  const el = useRef<HTMLDivElement>(null);
  const lib = useRef<typeof Leaflet | null>(null);
  const map = useRef<Leaflet.Map | null>(null);
  const pin = useRef<Leaflet.Marker | null>(null);
  // The last point picked here: the map is already looking at it.
  const picked = useRef<{ lat: number; lon: number } | null>(null);
  const [ready, setReady] = useState(false);

  // Handlers bound once at setup call the latest onPick.
  const pickRef = useRef(onPick);
  useEffect(() => {
    pickRef.current = onPick;
  });

  // Leaflet touches window on import, so it loads in the browser only.
  useEffect(() => {
    let cancelled = false;
    let m: Leaflet.Map | null = null;
    import("leaflet").then((mod) => {
      if (cancelled || !el.current) return;
      const L = mod.default ?? mod;
      m = L.map(el.current, { center: HOME, zoom: HOME_ZOOM, maxZoom: MAX_ZOOM });
      L.tileLayer(`${API_BASE}/api/tiles/{z}/{x}/{y}.png`, {
        maxZoom: MAX_ZOOM,
        attribution: ATTRIBUTION,
      }).addTo(m);
      m.on("click", (e: Leaflet.LeafletMouseEvent) => {
        picked.current = { lat: e.latlng.lat, lon: e.latlng.lng };
        pickRef.current(e.latlng.lat, e.latlng.lng);
      });
      lib.current = L;
      map.current = m;
      setReady(true);
    });
    return () => {
      cancelled = true;
      m?.remove();
      map.current = null;
      pin.current = null;
    };
  }, []);

  // Follow the chosen place: move the pin, and fly there when it was
  // chosen by text (a picked point is already in view).
  const lat = place?.lat;
  const lon = place?.lon;
  useEffect(() => {
    const L = lib.current;
    const m = map.current;
    if (!ready || !L || !m) return;
    if (lat === undefined || lon === undefined) {
      pin.current?.remove();
      pin.current = null;
      return;
    }
    if (pin.current) pin.current.setLatLng([lat, lon]);
    else {
      const marker = L.marker([lat, lon], {
        draggable: true,
        icon: L.divIcon({
          className: "",
          html: '<span class="block size-5 rounded-full border-[3px] border-white bg-sky-500 shadow-lg shadow-black/50"></span>',
          iconSize: [20, 20],
          iconAnchor: [10, 10],
        }),
      }).addTo(m);
      marker.on("dragend", () => {
        const p = marker.getLatLng();
        picked.current = { lat: p.lat, lon: p.lng };
        pickRef.current(p.lat, p.lng);
      });
      pin.current = marker;
    }
    if (!picked.current || !near(picked.current, { lat, lon })) {
      m.flyTo([lat, lon], Math.max(m.getZoom(), PLACE_ZOOM), { duration: 0.8 });
    }
    picked.current = null;
  }, [ready, lat, lon]);

  return (
    <div className="mt-4 w-full max-w-xl">
      <div
        ref={el}
        role="application"
        aria-label={t.map.label}
        className="isolate h-72 w-full overflow-hidden rounded-xl border border-slate-800 bg-slate-900"
      />
      <p className="mt-2 text-center text-xs text-slate-500">{t.map.hint}</p>
    </div>
  );
}
