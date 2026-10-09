import type { Place } from "./api";

// Recently chosen places, newest first. The coordinates are kept so picking
// one again lands on exactly the same spot; searching its name again could
// resolve to a different place. Forecasts are always fetched fresh.
const KEY = "raincast.recent.places";
const MAX = 10;

const isPlace = (v: unknown): v is Place =>
  typeof v === "object" &&
  v !== null &&
  typeof (v as Place).name === "string" &&
  typeof (v as Place).lat === "number" &&
  typeof (v as Place).lon === "number";

/** Same spot: same name within ~10 m. */
const same = (a: Place, b: Place) =>
  a.name.toLowerCase() === b.name.toLowerCase() &&
  Math.abs(a.lat - b.lat) < 1e-4 &&
  Math.abs(a.lon - b.lon) < 1e-4;

export function loadRecent(): Place[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? "[]");
    return Array.isArray(v) ? v.filter(isPlace).slice(0, MAX) : [];
  } catch {
    return [];
  }
}

function save(list: Place[]): Place[] {
  try {
    localStorage.setItem(KEY, JSON.stringify(list));
  } catch {
    // Storage unavailable (private mode); history just won't persist.
  }
  return list;
}

/** Moves place to the front, dropping an earlier copy of it (whose address it keeps when it has none, as from a link). */
export function addRecent(place: Place): Place[] {
  const list = loadRecent();
  const p: Place = { name: place.name, address: place.address ?? "", lat: place.lat, lon: place.lon };
  if (!p.address) p.address = list.find((q) => same(q, p))?.address ?? "";
  return save([p, ...list.filter((q) => !same(q, p))].slice(0, MAX));
}

export function clearRecent(): Place[] {
  return save([]);
}
