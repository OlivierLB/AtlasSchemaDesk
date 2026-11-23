const paths = {
  database:
    "M20 6c0 2-3.6 3-8 3S4 8 4 6s3.6-3 8-3 8 1 8 3Zm0 0v12c0 2-3.6 3-8 3s-8-1-8-3V6m0 6c0 2 3.6 3 8 3s8-1 8-3",
  search: "m21 21-5-5m2-6a7 7 0 1 1-14 0 7 7 0 0 1 14 0Z",
  home: "M3 3h7v7H3V3Zm11 0h7v7h-7V3ZM3 14h7v7H3v-7Zm11 0h7v7h-7v-7Z",
  sql: "m5 7 5 5-5 5m8 0h6",
  activity: "M2 12h5l3-8 4 16 3-8h5",
  settings: "M4 7h16M4 17h16M8 4v6m8 4v6",
  backup:
    "M4 16a4 4 0 0 1 0-8 7 7 0 0 1 13-2 5 5 0 0 1 3 10m-8 5V11m-4 4 4-4 4 4",
  arrow: "M5 12h14m-5-5 5 5-5 5",
  table: "M3 4h18v16H3V4Zm0 5h18M9 9v11",
  refresh: "M20 7V3m0 4h-4m4 0a9 9 0 1 0 1 9",
  menu: "M4 6h16M4 12h16M4 18h16",
  close: "m6 6 12 12M6 18 18 6",
} as const;

export function Icon({ name }: { name: keyof typeof paths }) {
  return (
    <svg
      className="icon"
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={paths[name]} />
    </svg>
  );
}
