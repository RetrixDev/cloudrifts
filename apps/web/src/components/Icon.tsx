import type { CSSProperties } from 'react';

const paths = {
  dashboard: 'M3.5 3.5h6v7h-6zM14.5 3.5h6v4h-6zM14.5 12.5h6v8h-6zM3.5 15.5h6v5h-6z',
  server: 'M5 3.5h14a1.5 1.5 0 0 1 1.5 1.5v4a1.5 1.5 0 0 1-1.5 1.5H5A1.5 1.5 0 0 1 3.5 9V5A1.5 1.5 0 0 1 5 3.5ZM5 13.5h14a1.5 1.5 0 0 1 1.5 1.5v4a1.5 1.5 0 0 1-1.5 1.5H5A1.5 1.5 0 0 1 3.5 19v-4A1.5 1.5 0 0 1 5 13.5ZM7 7h.01M7 17h.01M15 7h2M15 17h2',
  globe: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0ZM3 12h18M12 3c2.2 2.3 3.5 5.4 3.5 9s-1.3 6.7-3.5 9c-2.2-2.3-3.5-5.4-3.5-9S9.8 5.3 12 3Z',
  activity: 'M3 12h4l3-8 4 16 3-8h4',
  team: 'M15 21v-2a5 5 0 0 0-5-5H8a5 5 0 0 0-5 5v2M9 10a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7ZM16 4a3.5 3.5 0 0 1 0 7M21 21v-2a5 5 0 0 0-3-4.6',
  settings: 'M9 3h6l.7 3 2.6 1.5L21 7l2 5-2 5-2.7-.5-2.6 1.5-.7 3H9l-.7-3-2.6-1.5L3 17l-2-5 2-5 2.7.5L8.3 6 9 3ZM15.5 12a3.5 3.5 0 1 1-7 0 3.5 3.5 0 0 1 7 0Z',
  search: 'M16.5 10a6.5 6.5 0 1 1-13 0 6.5 6.5 0 0 1 13 0ZM15 15l5.5 5.5',
  bell: 'M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9ZM10 21h4',
  sun: 'M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0ZM12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.4 1.4M17.6 17.6 19 19M5 19l1.4-1.4M17.6 6.4 19 5',
  moon: 'M20.5 14A8.5 8.5 0 0 1 10 3.5 8.5 8.5 0 1 0 20.5 14Z',
  chevron: 'm9 5 7 7-7 7', down: 'm6 9 6 6 6-6', arrow: 'M4 12h16m-6-6 6 6-6 6',
  upRight: 'M6 18 18 6M6 6h12v12', plus: 'M12 5v14M5 12h14', close: 'm6 6 12 12M6 18 18 6',
  check: 'm5 12 4 4L19 6', shield: 'M12 3 20 6v5c0 5-3.5 8-8 10-4.5-2-8-5-8-10V6l8-3Zm-4 9 3 3 5-6',
  lock: 'M6 11h12a1 1 0 0 1 1 1v8H5v-8a1 1 0 0 1 1-1ZM8 11V7a4 4 0 0 1 8 0v4M12 15v2',
  backup: 'M3 10a9 9 0 1 1 0 5M3 4v6h6M12 7v5l3 2',
  folder: 'M3 7V5h6l2 3h10v11H3V7Z', database: 'M20 6c0 2-3.6 3-8 3S4 8 4 6s3.6-3 8-3 8 1 8 3ZM4 6v12c0 2 3.6 3 8 3s8-1 8-3V6M4 12c0 2 3.6 3 8 3s8-1 8-3',
  cpu: 'M7 7h10v10H7zM9 1v3M15 1v3M9 20v3M15 20v3M1 9h3M1 15h3M20 9h3M20 15h3',
  memory: 'M3 7h18v10H3zM7 10v4M11 10v4M15 10v4M7 17v3M12 17v3M17 17v3',
  network: 'M12 3v6M5 21v-6h14v6M12 15V9M9 3h6v5H9zM2 19h6v3H2zM16 19h6v3h-6z',
  storage: 'M5 4h14l3 13v3H2v-3L5 4ZM3 15h18M16 18h2M6 18h4',
  terminal: 'm5 6 6 6-6 6M13 18h6', help: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0ZM9 9a3 3 0 0 1 6 0c0 2-3 2-3 4M12 17h.01',
  book: 'M12 5c-4-3-7-2-10-1v15c4-1 7-1 10 1 3-2 6-2 10-1V4c-3-1-6-2-10 1ZM12 5v15',
  copy: 'M8 8h12v12H8zM15 4H4v11', external: 'M14 3h7v7M21 3 10 14M10 3H3v18h18v-7',
  filter: 'M4 7h16M7 12h10M10 17h4', clock: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0ZM12 7v5l3 2',
  warning: 'M12 8v5M12 17h.01M10.3 3.9 2 19h20L13.7 3.9a2 2 0 0 0-3.4 0Z',
  code: 'm7 7-5 5 5 5m10-10 5 5-5 5M14 4l-4 16', mail: 'M3 5h18v14H3zM3 5l9 8 9-8',
  git: 'M6 7v10M18 7c0 7-12 3-12 10M8 4a2 2 0 1 1-4 0 2 2 0 0 1 4 0ZM20 4a2 2 0 1 1-4 0 2 2 0 0 1 4 0ZM8 20a2 2 0 1 1-4 0 2 2 0 0 1 4 0Z',
  download: 'M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5', file: 'M5 3h9l5 5v13H5zM14 3v6h5',
  menu: 'M4 6h16M4 12h16M4 18h16', monitor: 'M3 3h18v13H3zM12 16v5M8 21h8',
} as const;
export type IconName = keyof typeof paths;
export function Icon({ name, size = 18, style, className = '' }: { name: IconName; size?: number; style?: CSSProperties; className?: string }) {
  return <svg width={size} height={size} className={`icon ${className}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.65" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={style}><path d={paths[name]} /></svg>;
}
export function CloudriftsMark({ className = '' }: { className?: string }) {
  return <svg className={`cloudrifts-mark ${className}`} viewBox="0 0 40 40" fill="none" aria-hidden="true"><path d="M9 23.5a7 7 0 0 1 .8-14 10.5 10.5 0 0 1 19.8 2.8 6.6 6.6 0 0 1 1 13.2H22l3.4-5.2h5a1.5 1.5 0 0 0 0-3H25l-.4-4a5.5 5.5 0 0 0-10.5-1.9l-1.4 3.3-3.4.1a1.8 1.8 0 0 0-.3 3.6h8.6L14 23.5H9Z" fill="currentColor"/><path d="m19 17-9 15h10l9-15H19Z" fill="currentColor" opacity=".5"/></svg>;
}
