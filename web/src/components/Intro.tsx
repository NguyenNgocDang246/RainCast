/** Shown until the user picks a place. */
export function Intro() {
  return (
    <section className="w-full max-w-xl text-center">
      <h1 className="text-3xl font-semibold leading-tight text-slate-50 sm:text-5xl">
        Mưa còn bao lâu nữa tới chỗ bạn?
      </h1>
      <p className="mt-4 text-lg text-slate-400">
        Xem trước cơn mưa trong một giờ tới, ngay tại nơi bạn đứng.
      </p>

      <ul className="mx-auto mt-10 grid max-w-md gap-3 text-left text-sm">
        <Example label="Địa chỉ" value="Chợ Bến Thành, Quận 1" />
        <Example label="Link Google Maps" value="Sao chép đường liên kết" />
        <Example label="Tọa độ" value="10.85, 106.77" />
      </ul>
    </section>
  );
}

function Example({ label, value }: { label: string; value: string }) {
  return (
    <li className="flex gap-3 rounded-xl border border-slate-800 bg-slate-900/40 px-4 py-3">
      <span className="w-32 shrink-0 text-slate-500">{label}</span>
      <span className="text-slate-200">{value}</span>
    </li>
  );
}
