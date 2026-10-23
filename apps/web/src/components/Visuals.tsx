import { useId, useState } from 'react';

export function ServerIllustration() {
  const id = useId().replace(/:/g, '');
  return <svg className="server-illustration" viewBox="0 0 380 250" role="img" aria-label="Cloudrifts server with three connected service layers">
    <defs>
      <linearGradient id={`${id}top`} x1="0" x2="1" y1="0" y2="1"><stop stopColor="#4b505c"/><stop offset=".5" stopColor="#373b48"/><stop offset="1" stopColor="#272c36"/></linearGradient>
      <linearGradient id={`${id}front`} x1="0" x2="1"><stop stopColor="#242935"/><stop offset="1" stopColor="#151a24"/></linearGradient>
      <linearGradient id={`${id}side`} x1="0" x2="1"><stop stopColor="#181b26"/><stop offset="1" stopColor="#30313f"/></linearGradient>
      <linearGradient id={`${id}trace`}><stop stopColor="#67ddd3" stopOpacity="0"/><stop offset=".5" stopColor="#67ddd3"/><stop offset="1" stopColor="#a49bff" stopOpacity=".15"/></linearGradient>
      <radialGradient id={`${id}glow`}><stop stopColor="#7362d0" stopOpacity=".28"/><stop offset="1" stopColor="#7362d0" stopOpacity="0"/></radialGradient>
      <filter id={`${id}shadow`} x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="9"/></filter>
      <filter id={`${id}led`}><feGaussianBlur stdDeviation="1.5"/></filter>
    </defs>
    <ellipse cx="201" cy="151" rx="172" ry="112" fill={`url(#${id}glow)`}/>
    <g stroke="#778092" strokeOpacity=".095" strokeWidth=".8">{Array.from({length:8},(_,i)=><g key={i}><path d={`M${-130+i*60} 175l240 115`}/><path d={`M${110+i*60} 145l-240 115`}/></g>)}</g>
    <path d="m19 179 85 41 67-33m85-25 71 36 39-19" stroke={`url(#${id}trace)`} strokeWidth="1.1" fill="none"/>
    <circle cx="19" cy="179" r="3" fill="#5ddfc4"/><circle cx="366" cy="179" r="3" fill="#9a88ff"/>
    <ellipse cx="198" cy="215" rx="98" ry="16" fill="#080b14" opacity=".6" filter={`url(#${id}shadow)`}/>
    {[131,94,57].map((y,i)=><g key={y}>
      <path d={`M85 ${y+38} 199 ${y+84} 298 ${y+36}v21l-99 48-114-46Z`} fill={`url(#${id}front)`} stroke="#484956" strokeWidth=".65"/>
      <path d={`m199 ${y+84} 99-48v21l-99 48Z`} fill={`url(#${id}side)`} stroke="#4a4b59" strokeWidth=".6"/>
      <path d={`m85 ${y+38} 98-48 115 46-99 48Z`} fill={`url(#${id}top)`} stroke="#676877" strokeWidth=".7"/>
      <path d={`m95 ${y+36} 88-43 103 43-87 42Z`} fill="none" stroke="#77798b" strokeOpacity=".16" strokeWidth=".6"/>
      <path d={`m95 ${y+47} 94 38`} stroke="#797d8f" strokeOpacity=".2"/>
      {[0,1,2,3,4,5,6,7].map(n=><path key={n} d={`m${109+n*6} ${y+56+n*2.42}v6`} stroke="#777d8b" strokeWidth="1.6" strokeOpacity=".38"/>)}
      <path d={`m226 ${y+79} 37-18`} stroke={i===2?'#a99afb':'#53d3b7'} strokeWidth="1.5" strokeLinecap="round"/>
      <path d={`m226 ${y+79} 37-18`} stroke={i===2?'#a99afb':'#53d3b7'} strokeWidth="3" filter={`url(#${id}led)`} opacity=".4"/>
      <circle cx="276" cy={y+55} r="1.5" fill="#a4edda"/>
    </g>)}
    <g transform="translate(165 55) skewY(22) scale(1 .7)" fill="none" stroke="#b9b2d3" strokeWidth="2" opacity=".65"><path d="M5 16c-8-2-8-13 0-15 5-10 22-6 22 5 9 0 11 13 1 15H10"/><path d="m18 8-8 17h11l8-17Z" fill="#b9b2d3" stroke="none"/></g>
  </svg>;
}

export function SiteThumbnail({ kind }: { kind: string }) {
  return <svg className={`site-thumbnail ${kind}`} viewBox="0 0 112 72" aria-hidden="true">
    {kind==='store' ? <><rect width="112" height="72" fill="#eeeae3"/><path d="M0 13h112" stroke="#d6d1c9"/><text x="9" y="9" fontSize="6" fontFamily="Georgia" fill="#454236">nōva.</text><path d="M81 7h7m4 0h5m4 0h4" stroke="#817b70" strokeWidth="1"/><text x="9" y="31" fontSize="8" fontFamily="Georgia" fill="#34362c">Objects for</text><text x="9" y="40" fontSize="8" fontFamily="Georgia" fill="#34362c">slower living.</text><rect x="9" y="49" width="24" height="5" rx="1" fill="#444b3c"/><path d="M77 25h14l-2 10c9 8 6 25-5 25S69 43 79 35Z" fill="#b49a7e"/><path d="M77 25h14v3H77z" fill="#96856e"/><ellipse cx="84" cy="62" rx="18" ry="2" fill="#d3ccbf"/><path d="M84 26c-2-12 9-12 7-19M84 27c3-9-9-11-7-19" fill="none" stroke="#677558" strokeWidth="1.5"/></> : kind==='studio' ? <><rect width="112" height="72" fill="#252625"/><text x="9" y="12" fill="#efece4" fontSize="7" fontFamily="Arial" fontWeight="bold">forma®</text><path d="M79 9h13m4 0h7" stroke="#b5b5aa"/><text x="8" y="36" fill="#e9e7da" fontSize="14" fontFamily="Arial" letterSpacing="-1">Make it</text><text x="8" y="51" fill="#e9e7da" fontSize="14" fontFamily="Arial" letterSpacing="-1">matter.</text><circle cx="86" cy="43" r="17" fill="#d8ec8c"/><ellipse cx="86" cy="43" rx="7" ry="17" fill="#b9cc73" transform="rotate(35 86 43)"/><path d="M9 62h21m5 0h10" stroke="#858677"/></> : kind==='journal' ? <><rect width="112" height="72" fill="#f3eee8"/><text x="40" y="12" fontSize="9" fontFamily="Georgia" fill="#40382e">Offgrid</text><path d="M8 17h96" stroke="#c7bbab"/><path d="M8 24h46v40H8Z" fill="#a6b39e"/><path d="m8 61 17-27 11 16 6-11 12 22" fill="#5f7564"/><circle cx="42" cy="32" r="5" fill="#ead9b1"/><text x="62" y="33" fontSize="6" fontFamily="Georgia" fill="#5b493b">A little closer</text><text x="62" y="41" fontSize="6" fontFamily="Georgia" fill="#5b493b">to the wild.</text><path d="M62 48h38m-38 4h33m-33 4h36" stroke="#c4bcad"/></> : <><rect width="112" height="72" fill="#e8edf6"/><text x="9" y="12" fontSize="7" fontFamily="Arial" fill="#3654a8">atlas.</text><path d="M71 9h31" stroke="#95a6c9"/><circle cx="80" cy="42" r="20" fill="#bacaf0"/><path d="m55 60 17-27 10 12 8-8 17 23" fill="#6f89c5"/><text x="9" y="33" fontSize="8" fontFamily="Arial" fill="#2b437d">Go beyond.</text><path d="M9 40h35m-35 4h28" stroke="#92a0bd"/><rect x="9" y="51" width="24" height="6" rx="2" fill="#4f6bc4"/></>}
  </svg>;
}

export function MiniChart({ variant = 0, color = 'currentColor' }: { variant?: number; color?: string }) {
  const paths = ['M0 29 7 27 14 29 21 22 28 23 35 20 42 22 49 12 56 15 63 9 70 12 77 6 84 8 92 2', 'M0 23 7 25 14 21 21 22 28 13 35 18 42 17 49 20 56 9 63 13 70 9 77 12 84 6 92 8', 'M0 28 7 28 14 23 21 23 28 20 35 20 42 20 49 15 56 15 63 11 70 11 77 11 84 5 92 5'];
  return <svg className="mini-chart" viewBox="0 0 94 34" fill="none" aria-hidden="true"><path d={paths[variant%3]} stroke={color} strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"/></svg>;
}

export function RiftSignature() {
  const id = useId().replace(/:/g, '');
  return <svg className="rift-signature" viewBox="0 0 520 118" aria-hidden="true">
    <defs>
      <linearGradient id={`${id}beam`} x1="0" x2="1"><stop stopColor="#8c78ff" stopOpacity="0"/><stop offset=".38" stopColor="#8c78ff" stopOpacity=".72"/><stop offset=".72" stopColor="#4ecbb2" stopOpacity=".58"/><stop offset="1" stopColor="#4ecbb2" stopOpacity="0"/></linearGradient>
      <linearGradient id={`${id}plate`} x1="0" x2="1" y1="0" y2="1"><stop stopColor="#ffffff" stopOpacity=".68"/><stop offset="1" stopColor="#ffffff" stopOpacity=".05"/></linearGradient>
      <filter id={`${id}blur`} x="-20%" y="-80%" width="140%" height="260%"><feGaussianBlur stdDeviation="4"/></filter>
    </defs>
    <g opacity=".8">
      <path d="M17 78h107l52-49h145l42 35h140" stroke={`url(#${id}beam)`} strokeWidth="1.4" fill="none"/>
      <path d="M5 99h89l46-43h93l37 31h112l31-24h92" stroke={`url(#${id}beam)`} strokeWidth=".9" fill="none" opacity=".65"/>
      <path d="M113 23h62l34 29h105l29-23h124" stroke={`url(#${id}beam)`} strokeWidth=".8" fill="none" opacity=".5"/>
      {[38,98,176,320,391,466].map((x,i)=><circle key={x} cx={x} cy={[78,99,29,64,87,63][i]} r={i===2?3.4:2.4} fill={i%2?'#53d5ba':'#9c8cff'}/>)}
    </g>
    <g transform="translate(228 38)">
      <path d="M0 18c-11-3-11-16 0-19 7-13 29-8 29 7 12 0 14 17 1 19H18l5-7h7a2 2 0 0 0 0-4h-8l-.6-5.6C20.1-2 5.8-.2 2.4 7.5L.5 12l-4.6.1a2.7 2.7 0 0 0-.5 5.3H8l-5 7H0Z" fill={`url(#${id}plate)`}/>
      <path d="m14 9-15 25h16L30 9H14Z" fill="#a99bff" opacity=".38"/>
    </g>
    <g filter={`url(#${id}blur)`} opacity=".7"><path d="M31 78h96l50-47h140l41 34h120" stroke={`url(#${id}beam)`} strokeWidth="5" fill="none"/></g>
  </svg>;
}

export function RiftTopology() {
  const id = useId().replace(/:/g, '');
  const nodes = [
    { x: 58, y: 45, tone: 'violet', kind: 'shop' },
    { x: 128, y: 24, tone: 'mint', kind: 'portfolio' },
    { x: 205, y: 42, tone: 'sand', kind: 'journal' },
    { x: 282, y: 25, tone: 'blue', kind: 'travel' },
    { x: 352, y: 50, tone: 'violet', kind: 'blog' },
    { x: 421, y: 31, tone: 'mint', kind: 'staging' },
  ];
  const SiteCard = ({ kind }: { kind: string }) => <g>
    <rect x="-24" y="-17" width="48" height="34" rx="9" fill={`url(#${id}fill)`}/>
    <rect x="-18" y="-10" width="36" height="22" rx="4" fill="#f2f4f8" stroke="currentColor" strokeOpacity=".15"/>
    <path d="M-18-4h36" stroke="currentColor" strokeOpacity=".16"/>
    <circle cx="-13" cy="-7" r="1.4" fill="currentColor" opacity=".55"/>
    {kind==='shop'&&<><rect x="-12" y="1" width="9" height="7" rx="1.5" fill="currentColor" opacity=".24"/><path d="M2 6h10M2 2h14" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"/></>}
    {kind==='portfolio'&&<><circle cx="-6" cy="3" r="6" fill="currentColor" opacity=".18"/><path d="M4-1h9M4 4h11M4 9h6" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"/></>}
    {kind==='journal'&&<><path d="M-13 9-5-1l5 7 4-5 9 8" fill="currentColor" opacity=".2"/><path d="M3-6h10M3-2h8" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/></>}
    {kind==='travel'&&<><circle cx="6" cy="2" r="7" fill="currentColor" opacity=".16"/><path d="M-12 8 0-4 6 2 12-5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/></>}
    {kind==='blog'&&<><rect x="-12" y="-1" width="10" height="10" rx="2" fill="currentColor" opacity=".18"/><path d="M3-4h10M3 1h12M3 6h8" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/></>}
    {kind==='staging'&&<><path d="M-10 7h20M-7 7V-3h14V7" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/><circle cx="0" cy="-6" r="2.8" fill="currentColor" opacity=".25"/></>}
  </g>;
  const BadgeIcon = ({ type }: { type: 'ssl' | 'backup' | 'dns' }) => <g>
    <rect width="42" height="20" rx="10"/>
    <circle cx="13" cy="10" r="3"/>
    {type==='ssl'&&<path d="M24 9.5 28 13l7-7" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/>}
    {type==='backup'&&<path d="M23 11a6 6 0 1 0 1-4M23 5v5h5" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>}
    {type==='dns'&&<path d="M24 6h9v7h-9zM28.5 13v4M25 17h7" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>}
  </g>;
  return <svg className="rift-topology" viewBox="0 0 480 128" role="img" aria-label="Cloudrifts website topology map">
    <defs>
      <linearGradient id={`${id}line`} x1="0" x2="1"><stop stopColor="#8f7bf0" stopOpacity=".1"/><stop offset=".45" stopColor="#8f7bf0" stopOpacity=".8"/><stop offset="1" stopColor="#61d2ba" stopOpacity=".65"/></linearGradient>
      <linearGradient id={`${id}fill`} x1="0" x2="1" y1="0" y2="1"><stop stopColor="#ffffff"/><stop offset="1" stopColor="#f6f3ff"/></linearGradient>
      <filter id={`${id}soft`} x="-60%" y="-80%" width="220%" height="260%"><feGaussianBlur stdDeviation="5"/></filter>
    </defs>
    <path d="M45 78 C95 34 141 85 188 55 S273 11 320 52 398 86 445 41" fill="none" stroke={`url(#${id}line)`} strokeWidth="1.3"/>
    <path d="M58 98 C139 63 199 104 265 75 S371 56 431 87" fill="none" stroke="#7f8fa3" strokeOpacity=".18" strokeDasharray="4 7"/>
    <g filter={`url(#${id}soft)`} opacity=".5"><path d="M47 80 C99 36 141 85 188 55 S273 11 320 52 398 86 445 41" fill="none" stroke="#8676e8" strokeWidth="7"/></g>
    <g className="topology-core" transform="translate(220 56)">
      <path d="m-44 18 45 20 49-22-46-19Z" fill="#262c36" stroke="#6a667e" strokeWidth=".8"/>
      <path d="m-44 1 45 20 49-22-46-19Z" fill="#35394a" stroke="#7d7890" strokeWidth=".8"/>
      <path d="m1 21 49-22v17L1 38Z" fill="#202631" stroke="#5c6375" strokeWidth=".7"/>
      <path d="m-44 1v17L1 38V21Z" fill="#1b202a" stroke="#505969" strokeWidth=".7"/>
      <path d="m-20 6 20 9 24-11" stroke="#9f91ff" strokeWidth="2" strokeLinecap="round"/>
      <path d="m16 21 19-9" stroke="#58d2bb" strokeWidth="1.7" strokeLinecap="round"/>
    </g>
    {nodes.map((node,i)=><g key={`${node.kind}-${node.x}`} transform={`translate(${node.x} ${node.y})`} className={`topology-node ${node.tone}`}>
      <SiteCard kind={node.kind}/>
      <circle r="4" cx="18" cy="-13" fill="currentColor"/>
      {i===3&&<path d="m-8 22 8 7 8-7" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/>}
    </g>)}
    <g className="topology-badges">
      <g transform="translate(61 99)"><BadgeIcon type="ssl"/></g>
      <g transform="translate(219 99)"><BadgeIcon type="backup"/></g>
      <g transform="translate(378 99)"><BadgeIcon type="dns"/></g>
    </g>
  </svg>;
}

export const metricSeries = {
  CPU: [13,16,15,22,17,17,26,21,18,19,14,18,15,23,20,25,17,20,18,33,26,24,30,18,19,28,22,17,19,28,25,17,21,18,29,23,33,26,31,39,34,25,28,32,27,23,28,19,23,18,24,20,15,23,18,14,19,16,18,12],
  Memory: [33,33,32,34,33,35,34,35,35,34,36,34,35,36,35,36,37,36,38,37,36,38,37,37,39,38,39,37,39,39,38,39,38,39,40,39,39,40,39,40,39,40,38,39,38,39,39,38,38,39,38,37,38,37,38,36,37,35,35,34],
  Network: [6,7,6,8,9,7,5,11,7,9,7,8,6,16,19,12,8,11,9,14,18,15,26,18,14,19,15,12,17,25,32,22,18,26,17,23,34,27,23,32,21,18,13,16,12,15,11,17,21,13,8,11,14,8,12,9,7,10,8,9],
};
export type MetricName = keyof typeof metricSeries;

export function PerformanceChart({ metric, range }: { metric: MetricName; range: string }) {
  const id=useId().replace(/:/g,'');
  const [hover,setHover]=useState<number|null>(null);
  const series=metricSeries[metric];
  const sampled = range==='1h' ? series : range==='24h' ? [...series.slice(20),...series.slice(0,20)] : [...series].reverse();
  const points=sampled.map((n,i)=>[i*600/(series.length-1),126-n*2.2]);
  let line=`M${points[0].join(',')}`;
  for(let i=1;i<points.length;i++){
    const prev=points[i-1],current=points[i],before=points[Math.max(0,i-2)],after=points[Math.min(points.length-1,i+1)];
    line+=` C${prev[0]+(current[0]-before[0])*.16},${prev[1]+(current[1]-before[1])*.16} ${current[0]-(after[0]-prev[0])*.16},${current[1]-(after[1]-prev[1])*.16} ${current.join(',')}`;
  }
  return <div className="performance-chart" aria-label={`${metric} usage, ${range}, sample data`} role="img"><div className="chart-y"><span>{metric==='Network'?'50 MB':'50%'}</span><span>{metric==='Network'?'25 MB':'25%'}</span><span>0</span></div><svg viewBox="0 0 600 135" preserveAspectRatio="none" onMouseMove={e=>{const rect=e.currentTarget.getBoundingClientRect();setHover(Math.min(sampled.length-1,Math.max(0,Math.round((e.clientX-rect.left)/rect.width*(sampled.length-1)))));}} onMouseLeave={()=>setHover(null)}><defs><linearGradient id={id} x1="0" x2="0" y1="0" y2="1"><stop offset="0" stopColor="#7b6cf0" stopOpacity=".19"/><stop offset="1" stopColor="#7b6cf0" stopOpacity=".015"/></linearGradient></defs>{[16,71,126].map(y=><path key={y} d={`M0 ${y}h600`} stroke="currentColor" className="chart-grid" strokeDasharray="3 5"/>)}<path d={`${line} L600 135 L0 135Z`} fill={`url(#${id})`}/><path d={line} fill="none" stroke="#8a77f2" strokeWidth="2.1" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke"/><circle cx="600" cy={126-sampled[sampled.length-1]*2.2} r="3" fill="#8a77f2"/>{hover!==null&&<g><path d={`M${points[hover][0]} 0v135`} stroke="#a99acb" strokeWidth=".8" strokeDasharray="3 3"/><circle cx={points[hover][0]} cy={points[hover][1]} r="4" stroke="var(--surface)" strokeWidth="2" fill="#8a77f2"/></g>}</svg>{hover!==null&&<span className="chart-tooltip" style={{left:`${Math.min(86,Math.max(12,hover/(sampled.length-1)*100))}%`}}>{sampled[hover]}{metric==='Network'?' MB/s':'%'}</span>}<div className="chart-x">{(range==='1h'?['13:00','13:15','13:30','13:45','14:00']:range==='24h'?['00:00','06:00','12:00','18:00','23:59']:['Mon','Tue','Wed','Thu','Fri']).map(x=><span key={x}>{x}</span>)}</div></div>;
}
