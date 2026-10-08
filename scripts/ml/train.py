"""Train the ML post-processing models from rows dumped by the backtest.

    go run ./cmd/backtest -paper -fresh -dump-features data/features.bin
    python scripts/ml/train.py data/features.bin [-train-until 2026-10-05T00:00:00Z]

For each feature set (-sets, by default radar and ens) and each strength (20, 30 and
40 dBZ) a LightGBM classifier learns P(echo >= strength) at a point and
lead. Every model is written as flat JSON for internal/ml (Go reads it; no
Python or cgo at run time):

  <set>_fold<k>.json  trained on the regions outside fold k, so the backtest
                      scores fold k with a model that never saw it; folds are
                      15-degree cells (fold_of), so neighbours, which share
                      storms at the same times, never sit on both sides
  <set>.json          trained on every region, for the app

Inside the training regions, a fifth of the regions (by name) is held out to
stop boosting, calibrate the probabilities (isotonic) and pick, per lead, the
probability above which the strength is forecast (best CSI, pooled with the
neighbouring leads so it does not jump from one lead to the next). Early
stopping looks at leads up to 60 minutes only: they are what the app shows.

With -train-until, only forecasts verified before that moment are learned
from; score the rest with `go run ./cmd/backtest -fresh -since <same>`.
The "radar" set is every radar and motion feature, the ML the backtest
reports. The "ens" set sees only the members' forecasts: the ensemble
calibrated the same way, the fair baseline for what the other features add.
"all", "no_nwp" and "no_sat" also read NWP and the satellite, which added
under a CSI point on held-out data (October 2026); they need a dump made
with -nwp-cache and -sat-cache, and cmd/collect -sat-cache for the tiles.

Run offline only; nothing here is deployed.
"""

import argparse
import datetime
import json
import math
import os
import sys
import zlib

import numpy as np

try:
    import lightgbm as lgb
    from sklearn.isotonic import IsotonicRegression
except ImportError:  # pragma: no cover
    sys.exit("pip install -r scripts/ml/requirements.txt")

CLASSES = [20.0, 30.0, 40.0]
SETS = {
    "radar": lambda n: not n.startswith(("nwp_", "sat_")),
    "all": lambda n: True,
    "no_nwp": lambda n: not n.startswith("nwp_"),
    "no_sat": lambda n: not n.startswith("sat_"),
    "ens": lambda n: n == "lead" or n.startswith("m_"),
}
MAIN_LEADS = 60  # early stopping and the summary look at leads up to this
PSTAR_GRID = np.round(np.arange(0.05, 0.951, 0.01), 2)
FOLD_CELL_DEG = 15  # ml.FoldCellDeg
# Folds ml.Fold gives these centers: fold_of must agree.
FOLD_CHECK = {(21.0, 105.8): 0, (-33.9, 151.2): 1, (40.7, -74.0): 0, (10.8, 106.7): 1}

PARAMS = dict(
    objective="binary",
    learning_rate=0.05,
    num_leaves=31,
    max_depth=8,
    min_data_in_leaf=500,
    feature_fraction=0.8,
    bagging_fraction=0.8,
    bagging_freq=1,
    lambda_l2=1.0,
    verbose=-1,
    seed=1,
    deterministic=True,
    force_col_wise=True,
)


def load(path):
    with open(path + ".json", encoding="utf-8") as f:
        meta = json.load(f)
    cols = meta["columns"]
    data = np.fromfile(path, dtype="<f4")
    if data.size % len(cols):
        sys.exit(f"{path}: {data.size} values is not whole rows of {len(cols)}")
    return meta, data.reshape(-1, len(cols))


def fold_of(lat, lon):
    """ml.Fold: the CRC-32 of the region's FOLD_CELL_DEG cell, mod 2."""
    cy, cx = math.floor(lat / FOLD_CELL_DEG), math.floor(lon / FOLD_CELL_DEG)
    return zlib.crc32(f"{cy},{cx}".encode()) % 2


def parse_time(s):
    """Unix seconds or an ISO 8601 time (UTC when no zone is given)."""
    try:
        return int(s)
    except ValueError:
        t = datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))
        if t.tzinfo is None:
            t = t.replace(tzinfo=datetime.timezone.utc)
        return int(t.timestamp())


def held_out(region_names):
    """A fifth of the regions, by a hash of the name (stable across runs)."""
    return np.array([zlib.crc32(n.encode()) % 5 == 0 for n in region_names])


def csi(y, pred, w):
    h = np.sum(w[pred & y])
    m = np.sum(w[~pred & y])
    f = np.sum(w[pred & ~y])
    return h / (h + m + f) if h + m + f > 0 else float("nan")


def flatten(tree):
    """LightGBM's nested tree as flat arrays, children after parents."""
    out = dict(feature=[], threshold=[], left=[], right=[], default_left=[], missing=[], value=[])
    miss = {"None": 0, "Zero": 1, "NaN": 2}

    def add(node):
        i = len(out["feature"])
        for k in out:
            out[k].append(0)
        if "leaf_value" in node or "split_feature" not in node:
            out["feature"][i] = -1
            out["value"][i] = float(node.get("leaf_value", 0.0))
            out["default_left"][i] = False
            out["threshold"][i] = 0.0
            return i
        if node.get("decision_type", "<=") != "<=":
            raise ValueError("categorical splits are not supported")
        out["feature"][i] = int(node["split_feature"])
        out["threshold"][i] = float(node["threshold"])
        out["default_left"][i] = bool(node["default_left"])
        out["missing"][i] = miss[node.get("missing_type", "None")]
        out["value"][i] = 0.0
        out["left"][i] = add(node["left_child"])
        out["right"][i] = add(node["right_child"])
        return i

    add(tree["tree_structure"])
    return out


def eval_flat(trees, x):
    """The Go evaluator in Python, to check the export."""
    total = 0.0
    for t in trees:
        i = 0
        while t["feature"][i] >= 0:
            v = x[t["feature"][i]]
            m = t["missing"][i]
            if m == 2 and math.isnan(v):
                left = t["default_left"][i]
            elif m == 1 and (math.isnan(v) or abs(v) <= 1e-35):
                left = t["default_left"][i]
            else:
                if math.isnan(v):
                    v = 0.0
                left = v <= t["threshold"][i]
            i = t["left"][i] if left else t["right"][i]
        total += t["value"][i]
    return total


def train_class(xtr, ytr, wtr, xva, yva, wva, lva):
    main = lva <= MAIN_LEADS
    dtr = lgb.Dataset(xtr, ytr.astype(np.float32), weight=wtr, free_raw_data=False)
    dva = lgb.Dataset(xva[main], yva[main].astype(np.float32), weight=wva[main], reference=dtr)
    booster = lgb.train(PARAMS, dtr, num_boost_round=1500, valid_sets=[dva],
                        callbacks=[lgb.early_stopping(50, verbose=False)])
    return booster


def fit(x, y, w, lead, val, names):
    """Train every class on ~val, calibrate and pick PStar on val."""
    classes = []
    report = {}
    for dbz in CLASSES:
        yy = y >= dbz
        booster = train_class(x[~val], yy[~val], w[~val], x[val], yy[val], w[val], lead[val])
        raw = booster.predict(x[val], raw_score=True, num_iteration=booster.best_iteration)
        p = 1 / (1 + np.exp(-raw))
        iso = IsotonicRegression(y_min=0, y_max=1, out_of_bounds="clip", increasing=True)
        iso.fit(p, yy[val], sample_weight=w[val])
        pc = iso.predict(p)
        leads = sorted(set(lead[val].tolist()))
        curves = []
        for L in leads:
            sel = lead[val] == L
            curves.append([csi(yy[val][sel], pc[sel] >= ps, w[val][sel]) for ps in PSTAR_GRID])
        curves = np.array(curves, dtype=np.float64)
        pstar, best = {}, {}
        for i, L in enumerate(leads):
            # The CSI curves of this lead and its neighbours, averaged: a
            # threshold picked on one lead alone jumps with the noise of
            # the few strong echoes, and the forecast flickers with it.
            near = curves[max(i - 1, 0):i + 2]
            valid = ~np.isnan(near)
            pooled = np.where(valid.any(axis=0), np.nansum(near, axis=0) / np.maximum(valid.sum(axis=0), 1), np.nan)
            k = int(np.nanargmax(pooled)) if valid.any() else len(PSTAR_GRID) // 2
            pstar[str(int(L))] = float(PSTAR_GRID[k])
            got = curves[i][k]
            best[str(int(L))] = None if math.isnan(got) else round(float(got), 4)
        dump = booster.dump_model(num_iteration=booster.best_iteration)
        trees = [flatten(t) for t in dump["tree_info"]]
        check(trees, booster, x[val])
        xs = [float(v) for v in iso.X_thresholds_]
        ys = [float(v) for v in iso.y_thresholds_]
        classes.append(dict(dbz=dbz, trees=trees, calib_x=xs, calib_y=ys, pstar=pstar))
        report[f">={int(dbz)}dBZ"] = dict(trees=len(trees), val_csi_by_lead=best)
    return classes, report


def check(trees, booster, x, n=300):
    """The flat trees must give LightGBM's own raw scores."""
    rng = np.random.default_rng(0)
    idx = rng.choice(len(x), size=min(n, len(x)), replace=False)
    want = booster.predict(x[idx], raw_score=True, num_iteration=booster.best_iteration)
    got = np.array([eval_flat(trees, row.astype(np.float64)) for row in x[idx]])
    diff = np.max(np.abs(want - got)) if len(idx) else 0.0
    if diff > 1e-4:
        sys.exit(f"exported trees disagree with LightGBM by {diff}")


def predict(bundle, x):
    """Calibrated, monotone class probabilities, as ml.Bundle.Predict."""
    ps = []
    for c in bundle["classes"]:
        p = 1 / (1 + math.exp(-eval_flat(c["trees"], x)))
        if c["calib_x"]:
            p = float(np.interp(p, c["calib_x"], c["calib_y"]))
        if ps and p > ps[-1]:
            p = ps[-1]
        ps.append(p)
    return ps


def write_parity(out, bundle, feats, names, n=40):
    """Rows (all features, dump order) and the probabilities the Go side
    must reproduce: internal/ml checks them against <set>.json."""
    rng = np.random.default_rng(1)
    idx = rng.choice(len(feats), size=min(n, len(feats)), replace=False)
    col = {nm: i for i, nm in enumerate(names)}
    rows, want = [], []
    for r in feats[idx]:
        x = np.array([r[col[f]] for f in bundle["features"]], dtype=np.float64)
        rows.append([None if math.isnan(v) else float(v) for v in r])
        want.append(predict(bundle, x))
    with open(os.path.join(out, "parity.json"), "w", encoding="utf-8") as f:
        json.dump(dict(set=bundle["set"], names=names, rows=rows, probs=want), f)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("dump", help="rows written by cmd/backtest -dump-features")
    ap.add_argument("-out", default="internal/ml/models", help="where the bundles go")
    ap.add_argument("-sets", default="radar,ens",
                    help="comma-separated feature sets: radar, ens, all, no_nwp, no_sat")
    ap.add_argument("-train-until", default="",
                    help="learn only from forecasts verified before this (unix seconds or ISO 8601); "
                         "then score with cmd/backtest -fresh -since <same>")
    args = ap.parse_args()
    for (la, lo), want in FOLD_CHECK.items():
        if fold_of(la, lo) != want:
            sys.exit(f"fold_of({la}, {lo}) = {fold_of(la, lo)}, ml.Fold says {want}")

    meta, data = load(args.dump)
    lead_n = meta["leading"]
    names = meta["columns"][lead_n:]
    regions = {r["id"]: r for r in meta["regions"]}
    if args.train_until:
        until = parse_time(args.train_until)
        # Issue time plus lead: the observation must be in the past too.
        verified = meta["t_epoch"] + data[:, 2].astype(np.int64) * 60 + data[:, 3].astype(np.int64) * 60
        data = data[verified < until]
        print(f"train until {datetime.datetime.fromtimestamp(until, datetime.timezone.utc):%Y-%m-%d %H:%M} UTC: "
              f"{len(data):,} rows kept")
        if not len(data):
            sys.exit("no rows before -train-until")
    region = data[:, 0].astype(int)
    # The rule, not the dumped column: dumps made before folds became
    # cells still train with today's folds.
    fold_by_id = {i: fold_of(r["lat"], r["lon"]) for i, r in regions.items()}
    fold = np.array([fold_by_id[i] for i in region])
    lead = data[:, 3]
    w = data[:, 4].astype(np.float64)
    obs = data[:, 5]
    feats = data[:, lead_n:].astype(np.float32)
    region_names = np.array([regions[i]["name"] for i in region])
    val_all = held_out(region_names)
    print(f"{len(data):,} rows, {len(regions)} regions, {int(np.sum(obs >= 20)):,} rainy")

    os.makedirs(args.out, exist_ok=True)
    summary = {"rows": int(len(data)), "regions": len(regions), "dump": os.path.basename(args.dump),
               "train_until": args.train_until or None}
    sets = args.sets.split(",")
    for set_name in sets:
        keep = [i for i, n in enumerate(names) if SETS[set_name](n)]
        fnames = [names[i] for i in keep]
        x = feats[:, keep]
        for k in (0, 1, -1):
            train = fold != k if k >= 0 else np.ones(len(data), bool)
            classes, rep = fit(x[train], obs[train], w[train], lead[train], val_all[train], fnames)
            bundle = dict(set=set_name, fold=k, features=fnames, classes=classes,
                          trained=f"{int(train.sum()):,} rows, {len(set(region[train].tolist()))} regions")
            name = f"{set_name}_fold{k}.json" if k >= 0 else f"{set_name}.json"
            with open(os.path.join(args.out, name), "w", encoding="utf-8") as f:
                json.dump(bundle, f, separators=(",", ":"))
            summary[name] = rep
            print(f"{name}: " + ", ".join(f"{c} {r['trees']} trees" for c, r in rep.items()))
            if set_name == sets[0] and k == -1:
                write_parity(args.out, bundle, feats, names)
    with open(os.path.join(args.out, "summary.json"), "w", encoding="utf-8") as f:
        json.dump(summary, f, indent=2)


if __name__ == "__main__":
    main()
