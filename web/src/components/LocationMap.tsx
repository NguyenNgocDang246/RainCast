"use client";

import "leaflet/dist/leaflet.css";
import type * as Leaflet from "leaflet";
import { useEffect, useRef, useState } from "react";
import { API_BASE, fetchRadar, type Place } from "@/lib/api";
import { pollFrames } from "@/lib/framePoll";
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
/** Browsers keep tiles for 30 days: change this when the server's -map-style changes. */
const TILE_STYLE = "osm-liberty";

const ATTRIBUTION =
  'Powered by <a href="https://www.geoapify.com/" target="_blank" rel="noreferrer">Geoapify</a> · ' +
  '© <a href="https://openmaptiles.org/" target="_blank" rel="noreferrer">OpenMapTiles</a> ' +
  '© <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer">OpenStreetMap</a> contributors';

/**
 * The radar frame forecasts start from, laid faintly over the map. The server
 * names the frame; browsers fetch its tiles from RainViewer themselves.
 */
const RADAR_OPACITY = 0.4;
const RADAR_ATTRIBUTION = 'Radar <a href="https://www.rainviewer.com/" target="_blank" rel="noreferrer">RainViewer</a>';

/**
 * A radar layer that, past the native zoom, stitches each tile's source
 * pixels from every native tile they span (plus a 1 px margin) before
 * scaling. Leaflet's maxNativeZoom scales each native tile on its own, and
 * the browser's interpolation stops at the image edge: a native tile 32x
 * enlarged shows a hard seam at every tile boundary.
 */
function radarLayer(L: typeof Leaflet, template: string, nativeZoom: number, options: Leaflet.GridLayerOptions) {
  let images = new Map<string, Promise<HTMLImageElement | null>>();
  // A missing tile leaves its part of the radar empty.
  const load = (url: string) => {
    let p = images.get(url);
    if (!p) {
      p = new Promise((resolve) => {
        const img = new Image();
        img.onload = () => resolve(img);
        img.onerror = () => resolve(null);
        img.src = url;
      });
      images.set(url, p);
    }
    return p;
  };
  const createTile = (coords: Leaflet.Coords, done: Leaflet.DoneCallback) => {
    const tile = document.createElement("canvas");
    tile.width = tile.height = 256;
    const z = Math.min(coords.z, nativeZoom);
    const s = 2 ** (coords.z - z); // display px per source px
    const n = 2 ** z;
    // The tile's source pixels at zoom z, in world pixels, with the margin.
    const sx = (coords.x * 256) / s;
    const sy = (coords.y * 256) / s;
    const x0 = Math.floor(sx) - 1;
    const y0 = Math.floor(sy) - 1;
    const x1 = Math.ceil(sx + 256 / s) + 1;
    const y1 = Math.ceil(sy + 256 / s) + 1;
    const src = document.createElement("canvas");
    src.width = x1 - x0;
    src.height = y1 - y0;
    const parts: Promise<void>[] = [];
    for (let ty = Math.floor(y0 / 256); ty <= Math.floor((y1 - 1) / 256); ty++) {
      if (ty < 0 || ty >= n) continue;
      for (let tx = Math.floor(x0 / 256); tx <= Math.floor((x1 - 1) / 256); tx++) {
        const url = L.Util.template(template, { z, x: ((tx % n) + n) % n, y: ty });
        parts.push(
          load(url).then((img) => {
            if (img) src.getContext("2d")!.drawImage(img, tx * 256 - x0, ty * 256 - y0);
          }),
        );
      }
    }
    Promise.all(parts).then(() => {
      // The whole source canvas, offset: a cropped drawImage may clamp its
      // interpolation at the crop.
      const ctx = tile.getContext("2d")!;
      ctx.imageSmoothingEnabled = true;
      ctx.imageSmoothingQuality = "high";
      ctx.drawImage(src, (x0 - sx) * s, (y0 - sy) * s, src.width * s, src.height * s);
      done(undefined, tile);
    });
    return tile;
  };
  const Layer = L.GridLayer.extend({ createTile }) as new (o: Leaflet.GridLayerOptions) => Leaflet.GridLayer;
  const layer = new Layer(options);
  const setTemplate = (url: string) => {
    if (url === template) return;
    template = url;
    images = new Map();
    layer.redraw();
  };
  return { layer, setTemplate };
}

/** Same point within ~1 m. */
const near = (a: { lat: number; lon: number }, b: { lat: number; lon: number }) =>
  Math.abs(a.lat - b.lat) < 1e-5 && Math.abs(a.lon - b.lon) < 1e-5;

export function LocationMap({ place, onPick }: Props) {
  const { t } = useLocale();
  const el = useRef<HTMLDivElement>(null);
  const lib = useRef<typeof Leaflet | null>(null);
  const map = useRef<Leaflet.Map | null>(null);
  const pin = useRef<Leaflet.Marker | null>(null);
  // The last point the pin was dragged to: the map is already looking at it.
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
    let stopRadar: (() => void) | undefined;
    // A click that closes the search's open list only closes it. Checked on
    // pointerdown, before the input loses focus.
    const container = el.current;
    let dismissing = false;
    const onDown = () => {
      dismissing = document.activeElement?.getAttribute("aria-expanded") === "true";
    };
    container?.addEventListener("pointerdown", onDown, { capture: true });
    import("leaflet").then((mod) => {
      if (cancelled || !el.current) return;
      const L = mod.default ?? mod;
      // Zoom sits bottom-right: the search box covers the top-left corner.
      m = L.map(el.current, { center: HOME, zoom: HOME_ZOOM, maxZoom: MAX_ZOOM, zoomControl: false });
      L.control.zoom({ position: "bottomright" }).addTo(m);
      L.tileLayer(`${API_BASE}/api/tiles/{z}/{x}/{y}.png?style=${TILE_STYLE}`, {
        maxZoom: MAX_ZOOM,
        attribution: ATTRIBUTION,
      }).addTo(m);
      const shown = m;
      let radar: ReturnType<typeof radarLayer> | null = null;
      const loadRadar = async () => {
        try {
          const f = await fetchRadar();
          if (cancelled) return null;
          if (radar) radar.setTemplate(f.tile_url);
          else {
            // Above max_zoom RainViewer has no tiles; the layer stretches the deepest.
            radar = radarLayer(L, f.tile_url, f.max_zoom, {
              opacity: RADAR_OPACITY,
              maxZoom: MAX_ZOOM,
              attribution: RADAR_ATTRIBUTION,
            });
            radar.layer.addTo(shown);
          }
          return f;
        } catch {
          // The radar is decoration; the map works without it.
          return null;
        }
      };
      stopRadar = pollFrames(loadRadar);
      m.on("click", (e: Leaflet.LeafletMouseEvent) => {
        if (dismissing) {
          dismissing = false;
          return;
        }
        pickRef.current(e.latlng.lat, e.latlng.lng);
      });
      lib.current = L;
      map.current = m;
      setReady(true);
    });
    return () => {
      cancelled = true;
      stopRadar?.();
      container?.removeEventListener("pointerdown", onDown, { capture: true });
      m?.remove();
      map.current = null;
      pin.current = null;
    };
  }, []);

  // Follow the chosen place: move the pin and fly there, zooming in, however
  // it was chosen (search, a click, the browser's position); a dragged pin
  // stays where it was dropped.
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

  // Fills its positioned parent; isolate keeps Leaflet's z-indexes below the overlays.
  return (
    <>
      <div ref={el} role="application" aria-label={t.map.label} className="absolute inset-0 isolate bg-slate-200" />
      <RadarLegend />
    </>
  );
}

/**
 * The radar's colors (RainViewer "Universal Blue", internal/radar/palette.csv)
 * in 5 dBZ steps, heaviest first, by hue: pink, red, yellow, blue. Red starts
 * at orange, 40 dBZ, where the forecast calls rain heavy (pipeline.DefaultConfig).
 */
const LEGEND: { level: "veryHeavy" | "heavy" | "moderate" | "light"; colors: string[] }[] = [
  { level: "veryHeavy", colors: ["#ffaaff"] }, // 55+
  { level: "heavy", colors: ["#c10000", "#ff4400", "#ffaa00"] }, // 50, 45, 40
  { level: "moderate", colors: ["#ffee00"] }, // 35
  { level: "light", colors: ["#005588", "#0077aa", "#00a3e0"] }, // 30, 25, 20
];

/**
 * How strongly the legend shows the colors: above RADAR_OPACITY, since the map's
 * own colors (roads, land) deepen the faint radar layer, and its swatches have none.
 */
const LEGEND_OPACITY = 0.65;

/** Which color is which rain, faint over a light chip as the map draws it. */
function RadarLegend() {
  const { t } = useLocale();
  return (
    // Hidden on phones, where the map is small and the forecast sheet covers it.
    <div className="pointer-events-none absolute right-4 top-1/2 z-10 hidden -translate-y-1/2 rounded-lg bg-slate-950/90 p-2 text-[11px] leading-none text-slate-300 shadow-lg sm:block">
      <p className="mb-1.5 font-medium text-slate-400">{t.map.legend}</p>
      <ul>
        {LEGEND.map(({ level, colors }) => (
          <li key={level} className="flex items-center gap-1.5">
            <span className="flex h-5 w-4 flex-col bg-slate-100">
              {colors.map((c) => (
                <span key={c} className="flex-1" style={{ background: c, opacity: LEGEND_OPACITY }} />
              ))}
            </span>
            {t.map.levels[level]}
          </li>
        ))}
      </ul>
    </div>
  );
}
