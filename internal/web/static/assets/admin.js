(function(){
  var root=document.getElementById('root'),logoutBtn=document.getElementById('logout'),timer=null;
  function el(t,c,x){var e=document.createElement(t);if(c)e.className=c;if(x!=null)e.textContent=x;return e}
  function api(method,path,body){
    return fetch('/admin/api'+path,{method:method,headers:{'Content-Type':'application/json','X-HyveMynd-Request':'1'},body:body?JSON.stringify(body):undefined,credentials:'same-origin'})
      .then(function(r){return r.json().catch(function(){return {}}).then(function(d){if(!r.ok){var e=new Error(d.error||('HTTP '+r.status));e.status=r.status;throw e}return d})});
  }
  function start(){
    stopLive();logoutBtn.hidden=true;document.body.classList.remove('wide');
    api('GET','/session').then(function(s){
      if(s.signedIn)return app();
      if(s.needsSetup)return setup();
      if(s.pending)return confirmStep();
      login();
    }).catch(function(e){root.replaceChildren(el('div','err',e.message))});
  }
  // Sign-in and first-run setup: one centred card with the icon and name above
  // it; setup steps use the two-bar step marker from the public Need access dialog.
  function signCard(title,step,steps){
    var w=el('div','hv-sign'),b=el('div','hv-sign-brand'),im=el('img');im.src='/assets/logo.svg';im.alt='';b.appendChild(im);b.appendChild(el('span','','HyveMynd'));w.appendChild(b);
    var c=el('section','hv-sign-card');c.appendChild(el('h2','',title));
    if(steps){var m=el('div','hv-steps');m.setAttribute('aria-label','step '+step+' of '+steps);for(var i=1;i<=steps;i++)m.appendChild(el('span',i<=step?'on':''));c.appendChild(m)}
    w.appendChild(c);root.replaceChildren(w);return c}
  function sfield(form,label,type,name,ph){var l=el('label','hv-f');l.appendChild(el('span','',label));var i=el('input');i.type=type||'text';i.name=name;if(ph)i.placeholder=ph;i.autocomplete=type==='password'?'current-password':'off';l.appendChild(i);form.appendChild(l);return i}
  function sbuttons(form,label){var r=el('div','hv-sign-row');var err=el('div','hv-err');err.setAttribute('role','alert');var b=el('button','hv-btn primary',label);b.type='submit';r.appendChild(err);r.appendChild(b);form.appendChild(r);return err}
  function login(){
    var c=signCard('Sign in');var f=el('form','hv-sign-form');var p=sfield(f,'Password','password','password');var k=sfield(f,'Authenticator code, or a recovery code','text','code');k.autocomplete='one-time-code';k.inputMode='numeric';
    var err=sbuttons(f,'Sign in');
    f.onsubmit=function(e){e.preventDefault();err.textContent='';api('POST','/login',{password:p.value,code:k.value}).then(start).catch(function(x){err.textContent=x.message})};
    c.appendChild(f);p.focus();
  }
  var pendingPw='';
  function setup(){
    var c=signCard('Set up HyveMynd',1,3);
    c.appendChild(el('p','hv-sign-help','First run. The setup key is printed in the container log, and saved in the config folder as setup-key.'));
    var f=el('form','hv-sign-form');var k=sfield(f,'Setup key','text','key');var p=sfield(f,'Choose a password (12 or more characters)','password','password');p.autocomplete='new-password';
    var err=sbuttons(f,'Continue');
    f.onsubmit=function(e){e.preventDefault();err.textContent='';api('POST','/setup',{key:k.value,password:p.value}).then(function(r){pendingPw=p.value;confirmStep(r)}).catch(function(x){err.textContent=x.message})};
    c.appendChild(f);k.focus();
  }
  function confirmStep(r){
    var c=signCard('Add an authenticator',2,3);
    if(r){
      c.appendChild(el('p','hv-sign-help','Add this secret to any authenticator app (TOTP), then enter the 6-digit code it shows.'));
      c.appendChild(el('pre','hv-secret',r.totpSecret));
      var a=el('a','hv-sign-link','Or open this link on your phone');a.href=r.otpauthUri;c.appendChild(a);
    }else c.appendChild(el('p','hv-sign-help','Setup was started but not finished. Enter your password and the code from your authenticator.'));
    var f=el('form','hv-sign-form');var p=null;if(!pendingPw)p=sfield(f,'Password','password','password');var k=sfield(f,'Code from the app','text','code');k.autocomplete='one-time-code';k.inputMode='numeric';
    var err=sbuttons(f,'Confirm');
    f.onsubmit=function(e){e.preventDefault();err.textContent='';api('POST','/setup/confirm',{password:pendingPw||p.value,code:k.value}).then(function(d){pendingPw='';
      var c3=signCard('Save your recovery codes',3,3);c3.appendChild(el('p','hv-sign-help','Each code works once if you lose your authenticator. They are not shown again.'));
      c3.appendChild(el('pre','hv-secret',d.recovery.join('\n')));var row=el('div','hv-sign-row');row.appendChild(el('span',''));var go=el('button','hv-btn primary','Continue to sign in');go.type='button';go.onclick=start;row.appendChild(go);c3.appendChild(row)}).catch(function(x){err.textContent=x.message})};
    c.appendChild(f);(p||k).focus();
  }
  // ---------------------------------------------------------------- app
  var tab=(location.hash||'').replace(/^#/,'')||'overview',detailId=null,actCount=null;
  function app(){
    logoutBtn.hidden=false;logoutBtn.onclick=function(){api('POST','/logout').then(start)};
    root.replaceChildren();
    document.body.classList.add('wide');
    // The menu follows what HyveMynd does beside the arrs: downloads in,
    // requests from visitors, the library it watches, and its settings.
    var NAV=[
      {id:'home',label:'Home',icon:'s_over',pages:[['overview','Home']]},
      {id:'downloads',label:'Downloads',icon:'s_dl',pages:[['jobs','Pipeline'],['torbox','TorBox'],['usenetq','Usenet'],['torrents','Torrents'],['rss','Feeds'],['add','Add a download']]},
      {id:'requests',label:'Requests',icon:'s_req',pages:[['requests','Requests']]},
      {id:'library',label:'Library',icon:'s_lib',pages:[['missing','Missing'],['upgrades','Upgrades'],['anime','Anime']]},
      {id:'settings',label:'Settings',icon:'s_sys',pages:[['start','Settings']],settings:true}];
    // Settings: one page with a menu in groups (pinned v4).
    var SETTINGS=[['','Getting started',[['start','Getting started']]],
      ['Connections',[['arrs','Sonarr · Radarr · categories','arr sonarr radarr lidarr category categories folders library'],['playback','Media server · Tracearr','emby jellyfin tracearr playback'],['public','Public page · Seerr','seerr requests donate public'],['transcode','Tdarr','tdarr transcode'],['metadata','Metadata','tmdb anime lists']]],
      ['Downloading',[['clients','TorBox','torbox token'],['usenet','Usenet providers','usenet news provider server connections'],['vpn','VPN and torrents','vpn wireguard pia proton mullvad nord torrent port'],['limits','Speed limits','speed limit bandwidth'],['reencode','Re-encode','av1 re-encode']]],
      ['Server',[['system','Server · sign-in · backups','password backup intake client login'],['log','Log','log']]]];
    var SETTING_IDS={};SETTINGS.forEach(function(g){(g[2]||g[1]).forEach(function(p){SETTING_IDS[p[0]]=p})});
    if(tab==='downloaders')tab='torbox';
    function first(p){return p[2]?p[2][0][0]:p[0]}
    var cur=null,curPage=null;NAV.forEach(function(sec){sec.pages.forEach(function(p){if(p[0]===tab||(p[2]&&p[2].some(function(q){return q[0]===tab}))){cur=sec;curPage=p}})});
    if(!cur&&SETTING_IDS[tab]){cur=NAV[4];curPage=cur.pages[0]}
    if(!cur){cur=NAV[0];curPage=cur.pages[0];tab='overview'}
    function go(t){stopLive();tab=t;detailId=null;try{history.replaceState(null,'','#'+t)}catch(x){}if(t==='anime'){aView.page='list'}app()}
    var shell=el('div','hv'),side=el('nav','hv-rail');side.setAttribute('aria-label','Sections');
    var brand=el('a','hv-brand');brand.href='/admin';brand.title='HyveMynd';var bi=el('img');bi.src='/assets/logo.svg';bi.alt='HyveMynd';brand.appendChild(bi);side.appendChild(brand);
    actCount=null;
    NAV.forEach(function(sec){var b=el('button','hv-sec'+(sec===cur?' on':''));b.type='button';b.appendChild(svg(sec.icon));b.appendChild(el('span','',sec.label));if(sec===cur)b.setAttribute('aria-current','page');
      if(sec.id==='requests'){actCount=el('span','hv-count');actCount.hidden=true;b.appendChild(actCount)}
      if(sec.id==='home'){homeCount=el('span','hv-count bad');homeCount.hidden=true;b.appendChild(homeCount)}
      if(sec.id==='downloads'){dlCount=el('span','hv-count dim');dlCount.hidden=true;b.appendChild(dlCount)}
      b.onclick=function(){go(first(sec.pages[0]))};tabBtns['sec:'+sec.id]=b;side.appendChild(b)});
    side.appendChild(el('span','hv-sp'));var pub=el('a','hv-sec hv-extra');pub.href='/';pub.appendChild(svg('s_pub'));pub.appendChild(el('span','','Public'));side.appendChild(pub);
    var so=el('button','hv-sec hv-extra');so.type='button';so.appendChild(svg('s_out'));so.appendChild(el('span','','Sign out'));so.onclick=function(){api('POST','/logout').then(start)};side.appendChild(so);
    api('GET','/requests').then(function(d){var n=(d.requests||[]).filter(function(r){return r.status==='pending'||r.Status==='pending'}).length+(d.music||[]).length;if(n&&actCount){actCount.textContent=n;actCount.hidden=false;actCount.setAttribute('aria-label',n+' requests waiting')}}).catch(function(){});
    api('GET','/jobs').then(function(d){railCounts(Array.isArray(d)?d:(d.jobs||[]))}).catch(function(){});
    var mainCol=el('div','hv-main'),head=el('header','hv-head');head.appendChild(el('h1','',cur.label));
    if(cur.pages.length>1&&!cur.settings){var pl=el('div','hv-pills');pl.setAttribute('role','tablist');cur.pages.forEach(function(p){var b=el('button',p===curPage?'on':'',p[1]);b.type='button';b.setAttribute('role','tab');b.setAttribute('aria-selected',p===curPage?'true':'false');b.onclick=function(){go(first(p))};tabBtns[p[0]]=b;pl.appendChild(b)});head.appendChild(pl)}
    var out=el('button','hv-out','Sign out');out.type='button';out.onclick=function(){api('POST','/logout').then(start)};head.appendChild(out);
    mainCol.appendChild(head);
    if(curPage[2]){var tb=el('div','hv-tabs');tb.setAttribute('role','tablist');curPage[2].forEach(function(q){var b=el('button',q[0]===tab?'on':'',q[1]);b.type='button';b.setAttribute('role','tab');b.setAttribute('aria-selected',q[0]===tab?'true':'false');b.onclick=function(){go(q[0])};tabBtns[q[0]]=b;tb.appendChild(b)});mainCol.appendChild(tb)}
    var bodyWrap=el('div','hv-body');mainCol.appendChild(bodyWrap);
    var main=el('main','main'),body=el('div');main.appendChild(body);bodyWrap.appendChild(main);shell.appendChild(side);shell.appendChild(mainCol);root.appendChild(shell);
    if(cur.settings){settingsShell(body,SETTINGS,go);}
    else if(tab==='overview')overviewView(body);else if(tab==='jobs')jobsView(body);else if(tab==='requests')requestsView(body);else if(tab==='missing')missingView(body);else if(tab==='upgrades')upgradesView(body);else if(tab==='anime')animeView(body);
    else if(tab==='torbox'||tab==='usenetq'||tab==='torrents')clientsView(body,tab);else if(tab==='add')addView(body);else settingsView(body);
  }
  var homeCount=null,dlCount=null;
  function railCounts(rows){var bad=rows.filter(function(r){return r.attention&&r.state!=='cleaned'}).length,mv=rows.filter(function(r){return WORKING[r.group]}).length;
    if(homeCount){homeCount.textContent=bad;homeCount.hidden=!bad;homeCount.setAttribute('aria-label',bad+' need you')}if(dlCount){dlCount.textContent=mv;dlCount.hidden=!mv;dlCount.setAttribute('aria-label',mv+' moving')}}
  // The settings page: a menu in groups with a search box, and the chosen page beside it.
  function settingsShell(body,groups,go){
    var wrap=el('div','hv-set');var nav=el('nav','hv-setnav');nav.setAttribute('aria-label','Settings sections');var main=el('div','hv-setmain');wrap.appendChild(nav);wrap.appendChild(main);body.appendChild(wrap);
    var sw=el('label','hv-setsearch');sw.appendChild(svg('search'));var si=el('input');si.type='search';si.placeholder='Find a setting';si.setAttribute('aria-label','Find a setting');sw.appendChild(si);nav.appendChild(sw);
    var links=[];
    groups.forEach(function(g){var items=g[2]||g[1],title=g[2]?g[0]:g[0];var box=el('div','hv-setgrp');if(title)box.appendChild(el('div','hv-setgh',title.toUpperCase()));
      items.forEach(function(p){var a=el('button','hv-setlink'+(p[0]===tab?' on':''),p[1]);a.type='button';if(p[0]===tab)a.setAttribute('aria-current','page');a.onclick=function(){go(p[0])};a.dataset.k=(p[1]+' '+(p[2]||'')).toLowerCase();links.push(a);box.appendChild(a)});nav.appendChild(box)});
    si.oninput=function(){var q=si.value.trim().toLowerCase();links.forEach(function(a){a.hidden=!!q&&a.dataset.k.indexOf(q)<0});[].forEach.call(nav.querySelectorAll('.hv-setgrp'),function(g){g.hidden=![].some.call(g.querySelectorAll('.hv-setlink'),function(a){return !a.hidden})})};
    if(tab==='start')startView(main);else if(tab==='usenet')usenetView(main);else if(tab==='vpn')vpnView(main);else if(tab==='reencode')reencodeView(main);else if(tab==='log')logView(main);else if(tab==='limits')limitsView(main);else settingsView(main);
  }
  function fail(e){if(e.status===401){start()}else{var d=el('div','err',e.message);root.appendChild(d)}}
  function ago(t){var s=Math.max(0,(Date.now()-new Date(t))/1000);if(s<90)return Math.round(s)+'s';if(s<5400)return Math.round(s/60)+'m';if(s<172800)return Math.round(s/3600)+'h';return Math.round(s/86400)+'d'}
  var jobFilter={q:'',group:'all',source:'all'},lastRows=[],paused=false,es=null,tabBtns={};
  function stopLive(){clearInterval(timer);if(es){es.close();es=null}}
  function attentionCount(rows){return rows.filter(function(r){return r.attention&&r.state!=='cleaned'}).length}
  var WTORBOX=['Requested','Found','Download','Transfer','Convert','Ready'],WLOCAL=['Requested','Found','Download','Convert','Ready'];
  function jobActions(j){
    var act=el('div','acts');
    function b(cls,label,fn,title){var x=el('button',cls,label);if(title)x.title=title;x.onclick=fn;act.appendChild(x)}
    if(j.state==='failed'){b('go','retry',function(){doAction(j.id,'retry')});b('','clean up now',function(){doAction(j.id,'cleanup')})}
    if(j.state==='ready')b('go','mark imported',function(){doAction(j.id,'imported')});
    if(j.state==='imported')b('','clean up now',function(){doAction(j.id,'cleanup')});
    if(j.state!=='cleaned'&&j.state!=='failed'&&j.state!=='imported')b('','fail it',function(){if(confirm('Fail this job? The arr will treat the download as failed and may search again.'))doAction(j.id,'fail')},'report a failed download so the arr blocklists this release and grabs another');
    if(j.state!=='cleaned')b('bad','cancel',function(){if(confirm('Cancel and clean up this job?'))doAction(j.id,'cancel')});
    return act}
  // Small pieces of the HyveMynd look (pinned v2 admin).
  var IC={s_over:'<path d="M4 13h6V4H4zM14 20h6v-9h-6zM4 20h6v-4H4zM14 8h6V4h-6z"/>',s_lib:'<path d="M4 4h4v16H4zM10 4h4v16h-4zM16 5l3.5-1 2.5 15.5-3.5 1z"/>',s_act:'<path d="M3 12h4l3-8 4 16 3-8h4"/>',s_src:'<circle cx="12" cy="12" r="3"/><path d="M12 2v4M12 18v4M2 12h4M18 12h4M5 5l3 3M16 16l3 3M5 19l3-3M16 8l3-3"/>',s_sys:'<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',s_dl:'<path d="M12 3v12M7 10l5 5 5-5M4 20h16"/>',s_req:'<path d="M4 5h16v11H8l-4 4z"/><path d="M12 8v5M9.5 10.5h5"/>',s_pub:'<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>',s_out:'<path d="M15 4h4v16h-4M10 8l-4 4 4 4M6 12h11"/>',refresh:'<path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/>',search:'<circle cx="11" cy="11" r="7"/><path d="M20 20l-4-4"/>',user:'<circle cx="12" cy="8" r="4"/><path d="M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6"/>',plus:'<path d="M12 5v14M5 12h14"/>',edit:'<path d="M4 20h4L19 9l-4-4L4 16v4z"/>',filter:'<path d="M3 5h18l-7 8v6l-4 2v-8L3 5z"/>',book:'<path d="M6 3h12v18l-6-4-6 4V3z" fill="currentColor"/>',bookoff:'<path d="M6 3h12v18l-6-4-6 4V3z"/>',dl:'<path d="M12 4v11M7 10l5 5 5-5M5 20h14"/>',x:'<path d="M6 6l12 12M18 6L6 18"/>',warn:'<path d="M12 3l10 18H2L12 3z"/><path d="M12 10v5M12 18h.01"/>',folder:'<path d="M3 6h7l2 2h9v11H3z"/>',drive:'<rect x="3" y="12" width="18" height="7" rx="1"/><path d="M5 12l3-7h8l3 7"/>',rss:'<path d="M5 19h.01M5 12a7 7 0 0 1 7 7M5 5a14 14 0 0 1 14 14"/>',up:'<path d="M12 19V5M6 11l6-6 6 6"/>',down:'<path d="M12 5v14M6 13l6 6 6-6"/>',back:'<path d="M15 6l-6 6 6 6"/>',cal:'<rect x="3" y="5" width="18" height="16" rx="1.5"/><path d="M3 10h18M8 3v4M16 3v4"/>',list:'<path d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01"/>'};
  function svg(n,label){var s='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" '+(label?'role="img" aria-label="'+label+'"':'aria-hidden="true"')+'>'+IC[n]+'</svg>';var w=el('span','a-ic');w.innerHTML=s;return w}
  function hvPill(text,color,fill){var p=el('span','hv-pill'+(fill?' fill':''),text);if(color){p.style.borderColor=color;p.style.color=fill?'#151617':color;if(fill)p.style.background=color}return p}
  function hvBtn(label,icon,fn,primary){var b=el('button','hv-btn'+(primary?' primary':''));b.type='button';if(icon)b.appendChild(svg(icon));b.appendChild(el('span','',label));b.onclick=function(){fn(b)};return b}
  function hvBox(title){var s=el('section','hv-box');if(title)s.appendChild(el('h3','',title));return s}
  function hvPoster(url,title){var p=el('div','hv-poster');if(url){var i=el('img');i.src=url;i.alt='';i.loading='lazy';i.onerror=function(){i.remove();p.appendChild(el('span','hv-ph',title))};p.appendChild(i)}else p.appendChild(el('span','hv-ph',title));return p}
  function cap(t){return t?t.charAt(0).toUpperCase()+t.slice(1):t}
  // The segmented circle from the public dashboard, for job rows: six arcs
  // (Requested, Found, Download, Transfer, Convert, Ready), finished ones green.
  var STAGES6=['requested','found','download','transfer','convert','ready'];
  var SVGNS='http://www.w3.org/2000/svg';
  function svgN(t,a){var e=document.createElementNS(SVGNS,t);for(var k in a)e.setAttribute(k,a[k]);return e}
  function arcD(i,f0,f1){var r=22,d=Math.PI/180,a0=(i*60+5+50*(f0||0))*d,a1=(i*60+5+50*(f1==null?1:f1))*d;function p(a){return (30+r*Math.sin(a)).toFixed(2)+' '+(30-r*Math.cos(a)).toFixed(2)}return 'M'+p(a0)+' A'+r+' '+r+' 0 0 1 '+p(a1)}
  function phase(n,ms){n.style.animationDelay=-(Date.now()%ms)+'ms';return n}
  function segCircle(stage,state,progress,size,w){
    size=size||34;w=w||8;var at=STAGES6.indexOf(stage),pct=Math.round((progress||0)*100);
    var s=svgN('svg',{viewBox:'0 0 60 60',width:size,height:size,'class':'seg',role:'img','aria-label':state+' at '+stage+(pct&&state==='working'?', '+pct+'%':'')});
    function arc(i,cls,f0,f1){var p=svgN('path',{d:arcD(i,f0,f1),fill:'none','stroke-width':w});p.setAttribute('class',cls);s.appendChild(p);return p}
    var txt='',tc='rt';
    STAGES6.forEach(function(st,i){
      if(i<at||state==='done'){arc(i,'dn');return}
      if(i>at){arc(i,'trk');return}
      if(state==='working'||state==='waiting'){
        // Only the current section moves; its own progress is a thin ring on the inner edge.
        arc(i,'trk');arc(i,'cur k-'+st);var fl=arc(i,'flow k-'+st+(state==='waiting'?' slow':''));fl.setAttribute('stroke-width',2.6);phase(fl,state==='waiting'?2400:1200);
        if(state==='working'){var ci=svgN('circle',{cx:30,cy:30,r:15,fill:'none','stroke-width':2.2});ci.setAttribute('class','in-trk');s.appendChild(ci);
          if(pct>0){var dg=Math.PI/180,e=Math.min(359.9,pct*3.6);var ip=svgN('path',{d:'M30 15 A15 15 0 '+(e>180?1:0)+' 1 '+(30+15*Math.sin(e*dg)).toFixed(2)+' '+(30-15*Math.cos(e*dg)).toFixed(2),fill:'none','stroke-width':2.2,'stroke-linecap':'round'});ip.setAttribute('class','in k-'+st);s.appendChild(ip);txt=String(pct)}}}
      else if(state==='stalled'){phase(arc(i,'stall'),1100);txt='!';tc='rt y'}
      else if(state==='failed'){phase(arc(i,'fail'),1600);txt='×';tc='rt r'}});
    var t=svgN('text',{x:30,y:36,'text-anchor':'middle','class':tc});t.textContent=txt;s.appendChild(t);return s}
  var JOBSTAGE={received:['found','waiting'],submitted:['found','working'],downloading:['download','working'],pulling:['transfer','working'],downloaded:['convert','waiting'],queued_transcode:['convert','waiting'],transcoding:['convert','working'],verifying:['convert','working'],ready:['ready','waiting'],imported:['ready','done'],cleaned:['ready','done']};
  var FAILAT={download:'download',transfer:'transfer',convert:'convert',ready:'ready'};
  function jobStage(j){var m=JOBSTAGE[j.state];if(j.state==='failed')return [FAILAT[j.failedAt]||'download','failed'];if(!m)return ['found','waiting'];if(j.flag&&m[1]!=='done')return [m[0],'stalled'];return m}
  var VERB={requested:'Requested',found:'Found',download:'Downloading',transfer:'Transferring',convert:'Converting',ready:'Waiting on the arr'};
  var NOUN={requested:'Request',found:'Search',download:'Download',transfer:'Transfer',convert:'Convert',ready:'Import'};
  function jobStageText(j){var m=jobStage(j);if(m[1]==='done')return 'Imported';if(m[1]==='failed')return NOUN[m[0]]+' failed';if(m[1]==='stalled')return NOUN[m[0]]+' stalled';if(m[1]==='waiting')return m[0]==='ready'?'Waiting on the arr':VERB[m[0]]+', in line';var t=VERB[m[0]];if(m[0]==='download'&&j.progress)t+=' '+Math.round(j.progress*100)+'%';return t}
  var KIND={sonarr:['TV','var(--tv)'],radarr:['Movie','var(--movie)'],lidarr:['Music','var(--music)'],rss:['Feed','var(--anime)'],watch:['Folder','var(--anime)'],reencode:['Re-encode','var(--dim)']};
  function jobRow(j,onOpen){
    var m=jobStage(j);var r=el('div','hv-job'+(m[1]==='failed'?' failed':m[1]==='stalled'?' stalled':''));var k=KIND[j.source]||[j.source||'Job','var(--dim)'];r.style.setProperty('--lc',k[1]);
    r.appendChild(segCircle(m[0],m[1],j.progress,36,8));
    var n=el('div','hv-job-t');var a=el('button','hv-link',j.name);a.type='button';a.onclick=function(){onOpen(j)};n.appendChild(a);
    var why=(j.flag?j.flag+' ':'')+(j.reason&&j.state!=='cleaned'?j.reason:'');
    n.appendChild(el('div','hv-job-s'+(m[1]==='failed'?' bad':m[1]==='stalled'?' warn':''),why||(j.category+' · '+(j.backend||'-')+(j.transcodeNode?' · '+j.transcodeNode:''))));r.appendChild(n);
    r.appendChild(hvPill(k[0],k[1]));
    r.appendChild(el('span','hv-job-st'+(m[1]==='failed'?' bad':m[1]==='stalled'?' warn':''),jobStageText(j)));
    r.appendChild(el('span','hv-age',ago(j.changed||j.created)));
    var acts=(m[1]==='failed'||m[1]==='stalled'||j.state==='ready')?jobActions(j):el('div');acts.className='hv-acts';[].forEach.call(acts.querySelectorAll('button'),function(b){b.className='hv-btn sm'+(/\bgo\b/.test(b.className)?' primary':/\bbad\b/.test(b.className)?' bad':'');b.textContent=cap(b.textContent)});r.appendChild(acts);return r}
  var WORKING={queued:1,downloading:1,processing:1,finishing:1};
  // Downloads › Pipeline (pinned v4): problems first, a six-step bar per
  // download, and a details panel with a plain verdict and the timeline.
  var pipeFilter='all',pipeLog=false;
  function stepBar(j){var m=jobStage(j),at=STAGES6.indexOf(m[0]);var b=el('div','hv-steps');b.setAttribute('role','img');b.setAttribute('aria-label',jobStageText(j));
    STAGES6.forEach(function(st,i){var c=el('span','');if(i<at||m[1]==='done')c.className='dn';else if(i===at){c.className=m[1]==='failed'?'bad':m[1]==='stalled'?'warn':'cur k-'+st;if(m[1]==='working'&&j.progress)c.style.setProperty('--p',Math.round(j.progress*100)+'%')}b.appendChild(c)});return b}
  function jobsView(body){
    var top=el('div','hv-pipehead');body.appendChild(top);var note=el('div','notice');body.appendChild(note);
    var wrap=el('div','hv-pipe');var listCol=el('section','hv-pipelist');listCol.setAttribute('aria-label','Downloads');var det=el('aside','hv-pipedet');det.setAttribute('aria-label','Details');wrap.appendChild(listCol);wrap.appendChild(det);body.appendChild(wrap);
    var q=el('input');q.type='search';q.placeholder='Search';q.setAttribute('aria-label','Search downloads');q.value=jobFilter.q;q.oninput=function(){jobFilter.q=q.value;drawRows()};
    var pb=hvBtn('Pause intake','',function(){api('POST','/intake',{paused:!paused}).then(function(r){paused=r.paused;banner()}).catch(fail)});
    var addB=hvBtn('Add a download','plus',function(){tab='add';app()},true);
    var chips=el('div','hv-chips');chips.setAttribute('role','group');chips.setAttribute('aria-label','Show');top.appendChild(chips);top.appendChild(el('span','hv-sp'));top.appendChild(q);top.appendChild(pb);top.appendChild(addB);
    function banner(){note.textContent=paused?'Intake is paused: new grabs are accepted but held until you resume.':'';note.style.display=paused?'block':'none';pb.querySelector('span').textContent=paused?'Resume intake':'Pause intake'}
    function isProblem(r){return r.attention&&r.state!=='cleaned'}
    function isDoneToday(r){return (r.state==='imported'||r.state==='cleaned')&&new Date(r.changed)>new Date(Date.now()-864e5)}
    function groupOf(r){if(isProblem(r))return 'need';if(WORKING[r.group])return 'moving';if(r.state==='imported'||r.state==='cleaned')return 'done';return 'waiting'}
    var selected=null,logCache={};
    function row(j){var m=jobStage(j),g=groupOf(j);var r=el('button','hv-prow '+(m[1]==='failed'?'bad':m[1]==='stalled'?'warn':'')+(selected===j.id?' sel':''));r.type='button';
      var t=el('div','hv-prt');t.appendChild(el('b','',j.name));var k=KIND[j.source]||[j.source||'',''];var src=el('span','hv-prs',[k[0],j.category,j.backend?({torbox:'TorBox',usenet:'Usenet',local:'Torrent'}[j.backend]||j.backend):''].filter(Boolean).join(' · '));t.appendChild(src);r.appendChild(t);
      if(g!=='done')r.appendChild(stepBar(j));
      var why=g==='need'?((j.flag?j.flag+' ':'')+(j.reason||'')).trim():'';r.appendChild(el('span','hv-prl',why||jobStageText(j)));
      r.onclick=function(){selected=j.id;detailId=j.id;drawRows();drawDet()};return r}
    function drawRows(){
      if(tab!=='jobs')return;
      var base=lastRows.filter(function(r){return !jobFilter.q||r.name.toLowerCase().indexOf(jobFilter.q.toLowerCase())>=0});
      chips.replaceChildren();
      [['problems','Problems',base.filter(isProblem).length],['all','Everything',base.filter(function(r){return r.state!=='cleaned'||isDoneToday(r)}).length],['moving','Moving',base.filter(function(r){return WORKING[r.group]}).length],['done','Done today',base.filter(isDoneToday).length]].forEach(function(c){
        var b=el('button','hv-chip'+(pipeFilter===c[0]?' on':'')+(c[0]==='problems'&&c[2]?' bad':''),c[1]+' '+c[2]);b.type='button';b.setAttribute('aria-pressed',pipeFilter===c[0]?'true':'false');b.onclick=function(){pipeFilter=c[0];drawRows()};chips.appendChild(b)});
      var show=base.filter(function(r){if(pipeFilter==='problems')return isProblem(r);if(pipeFilter==='moving')return WORKING[r.group];if(pipeFilter==='done')return isDoneToday(r);return r.state!=='cleaned'||isDoneToday(r)});
      listCol.replaceChildren();
      if(!show.length){listCol.appendChild(el('div','hv-empty',lastRows.length?'Nothing here.':'No downloads yet.'));}
      [['need','NEEDS YOU','bad'],['moving','MOVING',''],['waiting','WAITING',''],['done','DONE','']].forEach(function(g){var rows=show.filter(function(r){return groupOf(r)===g[0]});if(!rows.length)return;
        listCol.appendChild(el('h2','hv-grph '+g[2],g[1]));rows.sort(function(a,b){return new Date(b.changed)-new Date(a.changed)}).forEach(function(j){listCol.appendChild(row(j))})});
      if(!selected||!lastRows.some(function(r){return r.id===selected})){var f=show.filter(isProblem)[0]||show[0];selected=f?f.id:null;drawDet()}
      railCounts(lastRows)}
    function drawDet(){det.replaceChildren();var j=lastRows.filter(function(r){return r.id===selected})[0];
      if(!j){det.appendChild(el('div','hv-empty','Pick a download to see what is happening with it.'));return}
      var m=jobStage(j),k=KIND[j.source]||[j.source||''];
      det.appendChild(el('div','hv-dsub',[({torbox:'TorBox',usenet:'Usenet',local:'Torrent'}[j.backend]||j.backend||'Not sent yet'),k[0],j.category].filter(Boolean).join(' · ')));det.appendChild(el('h2','hv-dt',j.name));
      var why=((j.flag?j.flag+' ':'')+(j.reason||'')).trim();
      var v=el('div','hv-verdict '+(m[1]==='failed'?'bad':m[1]==='stalled'?'warn':m[1]==='done'?'ok':''));
      v.appendChild(el('strong','',m[1]==='failed'?'This did not work':m[1]==='stalled'?'This looks stuck':m[1]==='done'?'Done':jobStageText(j)));
      if(why)v.appendChild(el('span','',why));else if(m[1]==='working'||m[1]==='waiting')v.appendChild(el('span','',m[1]==='waiting'?'Waiting its turn.':'Moving along.'));det.appendChild(v);
      var acts=jobActions(j);acts.className='hv-acts';[].forEach.call(acts.querySelectorAll('button'),function(b){b.className='hv-btn'+(/\bgo\b/.test(b.className)?' primary':/\bbad\b/.test(b.className)?' bad':'');b.textContent=cap(b.textContent)});if(acts.children.length)det.appendChild(acts);
      var tl=el('ol','hv-tl');det.appendChild(tl);var logBtn=el('button','hv-linkbtn',pipeLog?'Hide the full log':'Show the full log');logBtn.type='button';logBtn.onclick=function(){pipeLog=!pipeLog;drawDet()};det.appendChild(logBtn);
      var pre=el('div','hv-log');if(pipeLog)det.appendChild(pre);det.appendChild(el('div','hv-rel',j.name));
      function fill(L){tl.replaceChildren();pre.replaceChildren();if(!L||L.error){tl.appendChild(el('li','hv-note',L&&L.error||'loading…'));return}
        (L.lines||[]).filter(function(l){return l.from!=='client'}).forEach(function(l){var li=el('li','');li.appendChild(el('span','t',new Date(l.at).toLocaleString([],{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'})));li.appendChild(el('span','m',l.msg));tl.appendChild(li)});
        (L.lines||[]).forEach(function(l){var r=el('div',l.from==='client'?'c':'p');r.appendChild(el('span','t',new Date(l.at).toLocaleTimeString()));r.appendChild(document.createTextNode(l.msg));pre.appendChild(r)})}
      fill(logCache[j.id]);api('GET','/clients/'+encodeURIComponent(j.id)+'/log').then(function(L){logCache[j.id]=L;if(selected===j.id)fill(L)}).catch(function(e){fill({error:e.message})})}
    var detSig='';
    function sig(){var j=lastRows.filter(function(r){return r.id===selected})[0];return j?j.state+'|'+j.reason+'|'+j.flag:''}
    function after(){drawRows();var x=sig();if(x!==detSig){detSig=x;drawDet()}}
    function take(d){lastRows=d.jobs||[];paused=!!d.paused;banner();after()}
    function poll(){api('GET','/jobs').then(function(rows){lastRows=Array.isArray(rows)?rows:(rows.jobs||[]);after()}).catch(fail)}
    if(detailId)selected=detailId;
    if(window.EventSource){
      es=new EventSource('/admin/api/events');
      es.addEventListener('jobs',function(e){try{take(JSON.parse(e.data))}catch(x){}});
      es.onerror=function(){if(es&&es.readyState===2){es.close();es=null;poll();timer=setInterval(poll,3000)}};
    }else{poll();timer=setInterval(poll,3000)}
    banner();drawRows();drawDet();
  }

  var reqAuto=null,reqMusic=[],reqMusicOn=false;
  function loadReqs(){return api('GET','/requests').then(function(d){if(d.auto)reqAuto=d.auto;reqMusic=d.music||[];reqMusicOn=!!d.musicOn;return d.requests||[]}).catch(function(){return null})}
  function reqRow(r,done,compact){
    var row=el('div',compact?'orow rrow':'jrow rrow');
    var n=el('div','jname');n.appendChild(el('div','',(r.title||'Untitled request')));
    var who=[r.media_type==='tv'?'TV show':'Movie',r.requested_by,r.seasons&&r.seasons.length?'seasons '+r.seasons.join(', '):''].filter(Boolean).join(' · ');
    n.appendChild(el('div','reason',who));row.appendChild(n);
    var a=el('div','acts rhoriz');var ap=el('button','go','Approve');ap.onclick=function(){ap.disabled=true;decideReq(r,'approve',done)};var dc=el('button','','Decline');dc.onclick=function(){dc.disabled=true;decideReq(r,'decline',done)};a.appendChild(ap);a.appendChild(dc);row.appendChild(a);return row}
  function setReqBadge(n){if(actCount){actCount.textContent=n||'';actCount.hidden=!n}if(!tabBtns.requests)return;tabBtns.requests.textContent='Media';if(n){var bd=el('span','badge',String(n));tabBtns.requests.appendChild(bd)}}
  function logView(body){
    body.appendChild(el('h2','ptitle','Log'));
    body.appendChild(el('p','hint','The container log since HyveMynd last started, newest at the bottom. Type a job id (the short code after "job"), or words like tdarr or failed, to narrow it. The last 2000 lines are kept.'));
    var bar=el('div','rebar');var q=el('input');q.placeholder='filter';q.style.minWidth='220px';bar.appendChild(q);
    var fl=el('label','','');var follow=el('input');follow.type='checkbox';follow.checked=true;fl.appendChild(follow);fl.appendChild(document.createTextNode(' follow'));bar.appendChild(fl);
    var info=el('span','msg');bar.appendChild(info);body.appendChild(bar);
    var pre=el('pre','logbox');pre.style.cssText='max-height:70vh;overflow:auto;white-space:pre-wrap;word-break:break-all;color:var(--fg,inherit);margin-top:14px;font-size:12px;line-height:1.45';body.appendChild(pre);
    var busy=false;
    function load(){
      if(busy)return;busy=true;
      api('GET','/log?n=500&q='+encodeURIComponent(q.value.trim())).then(function(r){
        var stick=follow.checked||pre.scrollTop+pre.clientHeight>=pre.scrollHeight-8;
        pre.textContent=r.lines.length?r.lines.join('\n'):'No lines match.';
        info.textContent=r.lines.length+' lines';
        if(stick)pre.scrollTop=pre.scrollHeight
      }).catch(fail).then(function(){busy=false})
    }
    q.oninput=load;load();timer=setInterval(function(){if(follow.checked)load()},3000)
  }
  function reencodeView(body){
    body.appendChild(el('h2','ptitle','Re-encode'));
    body.appendChild(el('p','hint','Finds videos in your library that are not AV1 and arrived since a date, so files that were imported before Tdarr finished can go back through it. Each one is copied, encoded and checked as AV1, and only then written over the library file. The original is left alone if anything fails.'));
    var bar=el('div','rebar');
    var d=new Date();d.setDate(d.getDate()-1);d.setHours(8,0,0,0);
    function local(dt){var z=new Date(dt.getTime()-dt.getTimezoneOffset()*60000);return z.toISOString().slice(0,16)}
    var lbl=el('label','','Added since ');var since=el('input');since.type='datetime-local';since.value=local(d);lbl.appendChild(since);bar.appendChild(lbl);
    var scan=el('button','go','Scan library');bar.appendChild(scan);body.appendChild(bar);
    var host=el('div');host.style.marginTop='16px';body.appendChild(host);
    var files=[],picked={};
    function size(n){if(!n)return '0 B';var u=['B','KB','MB','GB','TB'],i=0;while(n>=1024&&i<4){n/=1024;i++}return n.toFixed(i?1:0)+' '+u[i]}
    function draw(msg){
      host.replaceChildren();if(msg){host.appendChild(el('div','reason',msg))}
      if(!files.length){if(!msg)host.appendChild(el('div','empty','Nothing found. Scan to look.'));return}
      var open=files.filter(function(f){return !f.queued});var n=Object.keys(picked).filter(function(k){return picked[k]}).length;
      var head=el('div','rehead');var all=el('input');all.type='checkbox';all.checked=open.length>0&&n===open.length;all.onchange=function(){open.forEach(function(f){picked[f.path]=all.checked});draw(msg)};
      var al=el('label','','');al.appendChild(all);al.appendChild(document.createTextNode(' '+files.length+' not AV1 · '+n+' selected'));head.appendChild(al);
      var q=el('button','go','Queue '+n+' for re-encode');q.disabled=!n;q.onclick=function(){q.disabled=true;
        api('POST','/reencode',{paths:Object.keys(picked).filter(function(k){return picked[k]})}).then(function(r){
          files.forEach(function(f){if(picked[f.path])f.queued=true});picked={};
          draw('Queued '+r.queued+(r.skipped&&r.skipped.length?', skipped '+r.skipped.length+': '+r.skipped.slice(0,3).join('; '):'')+'. Follow them under Jobs.')}).catch(function(e){q.disabled=false;draw(e.message)})};
      head.appendChild(q);host.appendChild(head);
      var box=el('div','jlist');
      files.forEach(function(f){var row=el('div','jrow rerow');var c=el('input');c.type='checkbox';c.disabled=f.queued;c.checked=!!picked[f.path];c.onchange=function(){picked[f.path]=c.checked;draw(msg)};
        var nm=el('div','jname');nm.appendChild(el('div','',f.rel));
        nm.appendChild(el('div','reason',[f.codec==='unknown'?'codec unknown'+(f.error?' ('+f.error+')':''):f.codec,size(f.size),'added '+ago(f.changed)+' ago',f.queued?'queued':''].filter(Boolean).join(' · ')));
        row.appendChild(c);row.appendChild(nm);box.appendChild(row)});
      host.appendChild(box)}
    scan.onclick=function(){scan.disabled=true;picked={};host.replaceChildren(el('div','reason','Scanning the library. This can take a minute.'));
      api('GET','/reencode?since='+encodeURIComponent(new Date(since.value).toISOString())).then(function(r){files=r.files||[];scan.disabled=false;draw(files.length?'':'Everything added since then is AV1.')}).catch(function(e){scan.disabled=false;if(e.status===401){start()}else{host.replaceChildren(el('div','err',e.message))}})};
    draw();
  }
  // Requests (pinned v2 admin): waiting requests as cards with Approve and
  // Decline (with a reason), the auto-approve switch, then recent decisions.
  function decideReq(r,action,done,reason){api('POST','/requests/'+encodeURIComponent(r.source_id)+'/'+action+(reason?'?reason='+encodeURIComponent(reason):'')).then(done).catch(function(e){if(e.status===401)fail(e);else{alert(e.message);done()}})}
  var posterCache={};
  function reqPoster(kind,id,host,title){var k=kind+':'+id;function put(u){host.replaceChildren(hvPoster(u,title))}
    put('');if(!id)return;if(posterCache[k]!==undefined){put(posterCache[k]);return}
    fetch('/api/v1/media-request/title?type='+(kind==='movie'?'movie':'tv')+'&id='+id).then(function(r){return r.ok?r.json():{}}).then(function(d){posterCache[k]=d.poster||'';if(d.poster)put(d.poster)}).catch(function(){posterCache[k]=''})}
  function requestsView(body){
    var top=el('div','hv-pagehead');body.appendChild(top);var cards=el('div','hv-reqs');body.appendChild(cards);
    function drawTop(n){top.replaceChildren();top.appendChild(el('h2','',n===null?'Requests':n+' waiting'));top.appendChild(el('span','hv-sp'));top.appendChild(el('span','hv-note','Approving sends the request on in Seerr. Seerr keeps the history.'));
      if(reqAuto){var f=el('div','hv-auto');f.appendChild(el('span','hv-note','Approve automatically:'));[['movies','Movies'],['tv','TV shows']].concat(reqMusicOn?[['music','Music']]:[]).forEach(function(k){var l=el('label','');var i=el('input');i.type='checkbox';i.checked=!!reqAuto[k[0]];i.onchange=function(){var next={movies:reqAuto.movies,tv:reqAuto.tv,music:reqAuto.music};next[k[0]]=i.checked;api('PUT','/requests/auto',next).then(function(r){reqAuto=r.auto;if(r.approved)draw()}).catch(function(e){i.checked=!i.checked;alert(e.message)})};l.appendChild(i);l.appendChild(document.createTextNode(' '+k[1]));f.appendChild(l)});top.appendChild(f)}}
    function card(r){var c=el('article','hv-req');var ph=el('div','hv-req-poster');reqPoster(r.media_type,r.tmdb_id,ph,r.title||'');c.appendChild(ph);
      var b=el('div','hv-req-body');var tl=el('div','hv-pills-row');tl.appendChild(hvPill(r.media_type==='tv'?'Series':'Movie',r.media_type==='tv'?'var(--tv)':'var(--movie)',true));b.appendChild(tl);
      b.appendChild(el('h3','',r.title||'Untitled request'));
      var meta=[r.requested_by?'asked by '+r.requested_by:'',r.created_at&&r.created_at[0]!=='0'?ago(r.created_at)+' ago':''].filter(Boolean).join(' · ');if(meta)b.appendChild(el('div','hv-meta',meta));
      if(r.media_type==='tv')b.appendChild(el('div','hv-meta',r.seasons&&r.seasons.length?'Seasons '+r.seasons.join(', '):'Every season'));
      var a=el('div','hv-acts');a.appendChild(hvBtn('Approve','',function(x){x.disabled=true;decideReq(r,'approve',draw)},true));a.appendChild(hvBtn('Decline','',function(x){x.disabled=true;decideReq(r,'decline',draw)}));b.appendChild(a);c.appendChild(b);return c}
    function draw(){loadReqs().then(function(list){cards.replaceChildren();
      if(list===null){drawTop(null);cards.appendChild(el('div','hv-empty','Requests are not set up, or Seerr could not be reached. Set it under Settings › Public page.'));return}
      setReqBadge(list.length+reqMusic.length);drawTop(list.length+reqMusic.length);
      if(!list.length&&!reqMusic.length)cards.appendChild(el('div','hv-empty','Nothing is waiting for approval.'));
      list.forEach(function(r){cards.appendChild(card(r))});
      reqMusic.forEach(function(m){cards.appendChild(musicCard(m))})})}
    function musicCard(m){var c=el('article','hv-req');var ph=el('div','hv-req-poster hv-artist');if(m.image){var im=el('img');im.src=m.image;im.alt='';ph.appendChild(im)}else ph.appendChild(el('span','',(m.name||'?').slice(0,1)));c.appendChild(ph);
      var b=el('div','hv-req-body');var tl=el('div','hv-pills-row');tl.appendChild(hvPill('Music · whole artist','var(--music)',true));b.appendChild(tl);b.appendChild(el('h3','',m.name));
      var meta=[m.detail||'',m.requestedAt&&m.requestedAt[0]!=='0'?ago(m.requestedAt)+' ago':''].filter(Boolean).join(' · ');if(meta)b.appendChild(el('div','hv-meta',meta));
      var a=el('div','hv-acts');function go(action,x){x.disabled=true;api('POST','/music-requests/'+encodeURIComponent(m.id)+'/'+action).then(draw).catch(function(e){if(e.status===401)fail(e);else{alert(e.message);draw()}})}
      a.appendChild(hvBtn('Approve','',function(x){go('approve',x)},true));a.appendChild(hvBtn('Decline','',function(x){go('decline',x)}));b.appendChild(a);c.appendChild(b);return c}
    var acc=el('section','hv-accq');var ah=el('h2','hv-grph','ACCOUNT REQUESTS');body.appendChild(ah);body.appendChild(acc);accessCard(acc,'queue');
    draw();timer=setInterval(draw,15000)}
  // Anime (C3, C4): the show folders of the feed libraries, followed shows,
  // nyaa search and gap filling, in the arrs' style.
  var aView={page:'list',folder:null,filter:'all',q:''};
  function aBtn(icon,label,fn,cls){var b=el('button','a-tb'+(cls?' '+cls:''));b.type='button';b.appendChild(svg(icon));b.appendChild(el('span','',label));b.onclick=function(){fn(b)};return b}
  function aIcon(icon,label,fn){var b=el('button','a-iconbtn');b.type='button';b.setAttribute('aria-label',label);b.title=label;b.appendChild(svg(icon));b.onclick=function(e){e.stopPropagation();fn(b)};return b}
  function aLbl(text,cls){return el('span','a-lbl'+(cls?' '+cls:''),text)}
  function aSize(b){if(!b)return '';var u=['B','KB','MB','GB','TB'],i=0;while(b>=1024&&i<4){b/=1024;i++}return b.toFixed(i>2?1:0)+' '+u[i]}
  // The season bar from the public dashboard: one pixel per episode number from
  // the first to the newest of each season, green on disk, dim for a gap.
  // Anime season view: one segment per season (or per numbered run), its
  // width following the run's length; each fills to what is on disk, green
  // when complete, amber when numbers are missing. Absolute runs show their
  // range instead of a season.
  function aBar(sh){var s=sh.summary,w=el('div','a-ssw'),b=el('div','a-ss');var segs=(s.seasons||[]).filter(function(x){return x.count>0||x.last>0});
    var abs=segs.length===1&&segs[0].last>60;
    segs.forEach(function(ss){var from=(ss.first>1&&ss.first<=30)?1:ss.first,span=Math.max(1,ss.last-from+1),miss=(ss.gaps||[]).length,have=Math.max(0,span-miss);
      var g=el('span','a-seg'+(miss?' gap':''));g.style.flexGrow=String(Math.max(1,Math.sqrt(span)));
      // Each missing number is a dark notch where it falls in the run.
      (ss.gaps||[]).forEach(function(n){var k=el('u');k.style.left=(100*(n-from)/span).toFixed(2)+'%';k.style.width='max(2px,'+(100/span).toFixed(2)+'%)';g.appendChild(k)});
      var lbl=abs||!ss.season?from+'–'+ss.last:'S'+ss.season;g.appendChild(el('b','',lbl));
      g.title=(ss.season&&!abs?'Season '+ss.season+': ':'Episodes '+from+'–'+ss.last+': ')+ss.count+' on disk'+(miss?', missing '+(miss>12?ss.gaps.slice(0,12).join(', ')+' and '+(miss-12)+' more':ss.gaps.join(', ')):'');
      b.appendChild(g)});
    if(!segs.length)b.appendChild(el('span','a-seg none'));
    b.setAttribute('role','img');b.setAttribute('aria-label',s.files+' episodes on disk'+(s.gaps?', '+s.gaps+' missing':''));w.appendChild(b);
    w.appendChild(el('span','a-sscnt',String(s.files)));return w}
  function aStatus(sh){var a=sh.anilist;if(!a)return '';var m={RELEASING:'Airing',FINISHED:'Ended',NOT_YET_RELEASED:'Not aired yet',CANCELLED:'Cancelled',HIATUS:'On hiatus'}[a.status]||a.status;
    if(a.status==='RELEASING'&&a.nextAt){var d=Math.round((a.nextAt*1000-Date.now())/86400000);m+=' · ep '+a.nextEpisode+(d<=0?' today':d===1?' tomorrow':' in '+d+'d')}if(a.episodes)m+=' · '+a.episodes+' eps';return m}
  function aGroups(gs){var w=el('span','a-grp');(gs||[]).forEach(function(g,i){if(i)w.appendChild(el('span','a-arrow','›'));w.appendChild(aLbl(g))});return w}
  function aToolbar(items){var t=el('div','a-toolbar');t.setAttribute('role','toolbar');items.forEach(function(x){if(x==='|')t.appendChild(el('div','a-sep'));else if(x==='>')t.appendChild(el('div','a-grow'));else t.appendChild(x)});return t}
  function aMsg(){return el('div','a-msg')}
  function setMsg(m,text,bad){m.className='a-msg'+(bad?' bad':' ok');m.textContent=text}
  function animeView(body){var w=el('div','arr');body.appendChild(w);
    if(aView.page==='show')return aShowPage(w,aView.folder);if(aView.page==='add')return aAddPage(w);if(aView.page==='naming')return aNamingPage(w);aListPage(w)}
  function aListPage(w){
    var msg=aMsg(),host=el('div','a-content');
    w.appendChild(aToolbar([aBtn('refresh','Rescan folders',function(b){b.disabled=true;api('POST','/shows/rescan',{}).then(function(){setTimeout(app,600)}).catch(function(e){b.disabled=false;setMsg(msg,e.message,true)})}),'|',
      aBtn('plus','Add New',function(){aView.page='add';app()}),aBtn('edit','Naming',function(){aView.page='naming';app()}),'>',
      aBtn('filter',aView.filter==='following'?'Following':'All shows',function(){aView.filter=aView.filter==='following'?'all':'following';app()},aView.filter==='following'?'on':'')]));
    w.appendChild(host);host.appendChild(msg);
    var q=el('input','a-input');q.type='search';q.placeholder='Filter shows';q.value=aView.q;q.setAttribute('aria-label','filter shows');host.appendChild(q);
    var box=el('div','a-tw');host.appendChild(box);var note=el('div','a-dim a-small');host.appendChild(note);
    api('GET','/shows').then(function(d){
      if(!d.roots.length){box.appendChild(el('div','a-empty','No library folder is set. Give your anime category a library folder under Arrs & categories.'));return}
      function draw(){var list=d.shows.filter(function(s){return (aView.filter!=='following'||s.followed)&&(!aView.q||s.name.toLowerCase().indexOf(aView.q.toLowerCase())>=0||(s.titles||[]).join(' ').toLowerCase().indexOf(aView.q.toLowerCase())>=0)});
        box.replaceChildren();var t=el('table','a-t a-shows'),hr=el('tr');['','Show','Groups (in order)','Quality','Episodes on disk','Gaps','Latest','Status',''].forEach(function(h){hr.appendChild(el('th','',h))});var th=el('thead');th.appendChild(hr);t.appendChild(th);var tb=el('tbody');
        list.slice(0,400).forEach(function(s){var tr=el('tr');var c0=el('td');c0.appendChild(aFollow(s,function(){app()}));tr.appendChild(c0);
          var c1=el('td');var a=el('button','a-title',s.name);a.type='button';a.onclick=function(){aView.page='show';aView.folder=s.folder;app()};c1.appendChild(a);
          var sub=el('div','a-dim a-small',(s.titles&&s.titles.length?'also '+s.titles.join(', '):''));if(s.handMade&&!s.followed)sub.textContent=(sub.textContent?sub.textContent+' · ':'')+'hand-made feed: '+s.handMade;c1.appendChild(sub);tr.appendChild(c1);
          var c2=el('td');if(s.followed)c2.appendChild(aGroups(s.groups));tr.appendChild(c2);
          var c3=el('td');if(s.followed)c3.appendChild(aLbl(s.quality));tr.appendChild(c3);
          var c4=el('td');c4.appendChild(s.unscanned?el('span','a-dim a-small','not read yet'):aBar(s));tr.appendChild(c4);
          var c5=el('td');c5.appendChild(s.summary.gaps?aLbl(s.summary.gaps+' gap'+(s.summary.gaps>1?'s':''),'orange'):el('span','a-dim','—'));var on=(d.active||{})[s.folder];if(on){var pl=aLbl(on+' on the way','purple');pl.style.marginLeft='6px';c5.appendChild(pl)}tr.appendChild(c5);
          tr.appendChild(el('td','a-dim',s.summary.latest?((s.summary.season>1?'S'+s.summary.season+' · ':'')+s.summary.latest):'—'));
          var c7=el('td');if(s.followed)c7.appendChild(aLbl('Following','teal'));var st=aStatus(s);if(st)c7.appendChild(el('div','a-dim a-small',st));tr.appendChild(c7);
          var c8=el('td','a-acts');c8.appendChild(aIcon('search','interactive search: '+s.name,function(){aSearchModal(s,0)}));c8.appendChild(aIcon('edit','edit '+s.name,function(){aEditModal(s,function(){app()})}));tr.appendChild(c8);
          tb.appendChild(tr)});
        t.appendChild(tb);box.appendChild(t);
        var fol=d.shows.filter(function(s){return s.followed}).length,gaps=d.shows.reduce(function(n,s){return n+s.summary.gaps},0);
        note.textContent=d.shows.length+' folders · '+fol+' followed · '+gaps+' gaps · showing '+Math.min(list.length,400)+'. The bar has a segment per season, sized by its length: green when every episode is on disk, amber with a dark notch at each missing number. Status comes from AniList once a show has been opened or followed.'}
      q.oninput=function(){aView.q=q.value;draw()};draw();
      if(d.scan&&d.scan.running){setMsg(msg,'Reading folders… '+d.scan.done+' of '+(d.scan.total||'?')+'. The list fills in as it goes.');clearInterval(timer);timer=setInterval(function(){if(tab!=='anime'||aView.page!=='list'){clearInterval(timer);return}api('GET','/shows').then(function(x){d.shows=x.shows;d.active=x.active;draw();if(x.scan.running)setMsg(msg,'Reading folders… '+x.scan.done+' of '+(x.scan.total||'?')+'.');else{clearInterval(timer);msg.textContent=''}}).catch(function(){})},3000)}
    }).catch(function(e){if(e.status===401)fail(e);else setMsg(msg,e.message,true)})}
  function aFollow(s,after){var on=!!s.followed;var b=el('button','a-mon'+(on?'':' off'));b.type='button';b.setAttribute('aria-label',(on?'following: ':'not following: ')+s.name);b.title=on?'Following: click to stop':'Not following: click to follow';b.appendChild(svg(on?'book':'bookoff'));
    b.onclick=function(e){e.stopPropagation();b.disabled=true;api('PUT','/shows/one',{folder:s.folder,followed:!on,groups:s.groups,quality:s.quality||'1080p',titles:s.titles,fromEpisode:on?s.fromEpisode:0,anilistId:s.anilistId||0,fillGaps:s.followed?!!s.fillGaps:true}).then(after).catch(function(err){b.disabled=false;alert(err.message)})};return b}
  function aShowPage(w,folder){
    var msg=aMsg();var host=el('div');w.appendChild(host);
    api('GET','/shows/one?folder='+encodeURIComponent(folder)).then(function(d){var s=d.show;
      function act(b,path,body,done){b.disabled=true;setMsg(msg,'working…');api('POST','/shows/'+path,body).then(function(r){b.disabled=false;done(r)}).catch(function(e){b.disabled=false;setMsg(msg,e.message,true)})}
      host.appendChild(aToolbar([aBtn('back','Library',function(){aView.page='list';app()}),'|',aBtn('refresh','Rescan folder',function(){app()}),aBtn('edit','Tidy names',function(){aTidyModal(folder,s.name,function(){app()})}),
        aBtn('rss','Check now',function(b){if(!s.followed){setMsg(msg,'Follow the show first.',true);return}act(b,'check',{folder:folder},function(r){setMsg(msg,r.started.length?'Started: '+r.started.join(', '):'Nothing new to grab.');if(r.started.length)setTimeout(app,1500)})}),
        aBtn('search','Search gaps',function(b){if(!s.summary.gaps){setMsg(msg,'No gaps in this folder.');return}act(b,'gaps',{folder:folder},function(r){setMsg(msg,r.started.length?'Started: '+r.started.join(', '):'nyaa has nothing for the gaps in your group order and quality. Try interactive search.')})}),
        aBtn('user','Interactive search',function(){aSearchModal(s,0)}),'|',aBtn('edit','Edit',function(){aEditModal(s,function(){app()},d.defaultGroups)}),
        aBtn('cal','AniList',function(b){act(b,'anilist',{folder:folder},function(){app()})}),'>']));
      var hd=el('div','a-hdr');var h1=el('h1');h1.appendChild(aFollow(s,function(){app()}));h1.appendChild(el('span','',s.name));hd.appendChild(h1);
      var chips=el('div','a-chips');function chip(icon,text){var c=el('span','a-chip');if(icon)c.appendChild(svg(icon));c.appendChild(el('span','',text));chips.appendChild(c);return c}
      chip('folder',s.folder);chip('drive',aSize(s.summary.size));
      if(s.followed){chip(null,s.quality);var g=chip(null,'');g.replaceChildren(aGroups(s.groups));chip('rss',s.checked&&s.checked[0]!=='0'?'checked '+ago(s.checked)+' ago'+(s.checkError?' · '+s.checkError:''):'not checked yet');chips.appendChild(aLbl('Following','teal'))}
      else chips.appendChild(aLbl('Not following'));
      hd.appendChild(chips);
      if(s.anilist){var al=el('div','a-dim');var link=el('a','',(s.anilist.english||s.anilist.romaji)+' on AniList');link.href=s.anilist.url;link.target='_blank';link.rel='noopener';al.appendChild(link);al.appendChild(document.createTextNode(' · '+aStatus(s)+(s.anilist.season?' · '+s.anilist.season.toLowerCase()+' '+s.anilist.year:'')));hd.appendChild(al)}
      else hd.appendChild(el('div','a-dim','AniList has no match for the folder name. Set the AniList id in Edit to show its status.'));
      if(s.titles&&s.titles.length)hd.appendChild(el('div','a-dim','Also matched as: '+s.titles.join(', ')));
      if(s.followed)hd.appendChild(el('div','a-dim a-small','New episodes above '+(s.fromEpisode||0)+' are grabbed by themselves, from '+s.groups[0]+' first'+(s.groups.length>1?'; a later group only when an earlier one skips an episode':'')+'. '+(s.fillGaps?'Gaps are searched for once a day'+(s.gapsChecked&&s.gapsChecked[0]!=='0'?' (last '+ago(s.gapsChecked)+' ago)':'')+'.':'Gaps are only searched for by hand.')));
      host.appendChild(hd);var c=el('div','a-content');host.appendChild(c);c.appendChild(msg);
      var by={};d.episodes.forEach(function(e){(by[e.season]=by[e.season]||[]).push(e)});
      Object.keys(by).map(Number).sort(function(a,b){return b-a}).forEach(function(sn){var list=by[sn].sort(function(a,b){return b.episode-a.episode});
        var sec=el('section','a-season');var sh=el('div','a-season-h');sh.appendChild(el('h2','',sn===0?'Specials':(Object.keys(by).length>1||sn>1?'Season '+sn:'Episodes')));
        var have=list.filter(function(e){return e.state==='have'||(e.state==='processing'&&e.size)}).length,gaps=list.filter(function(e){return e.state==='gap'}).length,proc=list.filter(function(e){return e.state==='processing'}).length;sh.appendChild(el('span','a-dim',have+' on disk'+(gaps?' · '+gaps+' missing':'')+(proc?' · '+proc+' on the way':'')));sec.appendChild(sh);
        var t=el('table','a-t'),hr=el('tr');['','#','File','Quality','Size',''].forEach(function(h){hr.appendChild(el('th','',h))});var th=el('thead');th.appendChild(hr);t.appendChild(th);var tb=el('tbody');
        list.forEach(function(e){var tr=el('tr',e.state==='gap'?'a-miss':e.state==='processing'?'a-proc':'');var st=el('span','a-status '+e.state);st.setAttribute('aria-hidden','true');var c0=el('td');c0.appendChild(st);tr.appendChild(c0);tr.appendChild(el('td','',String(e.episode)));
          var f=el('td');if(e.state==='processing'){f.appendChild(el('span','a-proct',jobWord(e.job)));if(e.file)f.appendChild(el('div','a-mono a-dim',e.file))}else if(e.file)f.appendChild(el('span','a-mono',e.file));else f.appendChild(el('span','a-warnt','missing: a gap in the folder'));tr.appendChild(f);
          var q=el('td');if(e.res)q.appendChild(aLbl(e.res));tr.appendChild(q);tr.appendChild(el('td','a-dim',aSize(e.size)));
          var a=el('td','a-acts');a.appendChild(aIcon('user','interactive search for episode '+e.episode,function(){aSearchModal(s,e.episode)}));tr.appendChild(a);tb.appendChild(tr)});
        t.appendChild(tb);var tw=el('div','a-tw');tw.appendChild(t);sec.appendChild(tw);c.appendChild(sec)});
      if(!d.episodes.length)c.appendChild(el('div','a-empty','No episodes in this folder yet.'));
      if(d.seen.length){var sec=el('section','a-season');var sh2=el('div','a-season-h');sh2.appendChild(el('h2','','Releases seen'));sh2.appendChild(el('span','a-dim','what nyaa offered for this show, newest first'));sec.appendChild(sh2);
        var t2=el('table','a-t'),hr2=el('tr');['Seen','Title','Group','Quality','Size',''].forEach(function(h){hr2.appendChild(el('th','',h))});var th2=el('thead');th2.appendChild(hr2);t2.appendChild(th2);var tb2=el('tbody');
        d.seen.forEach(function(x){var tr=el('tr');tr.appendChild(el('td','a-dim',ago(x.seen)+' ago'));tr.appendChild(el('td','a-mono',x.title));var g=el('td');if(x.group)g.appendChild(aLbl(x.group));tr.appendChild(g);var q=el('td');if(x.res)q.appendChild(aLbl(x.res));tr.appendChild(q);tr.appendChild(el('td','a-dim',aSize(x.size)));var gr=el('td');if(x.grabbed&&x.grabbed[0]!=='0')gr.appendChild(aLbl('grabbed','green'));tr.appendChild(gr);tb2.appendChild(tr)});
        t2.appendChild(tb2);var tw2=el('div','a-tw');tw2.appendChild(t2);sec.appendChild(tw2);c.appendChild(sec)}
    }).catch(function(e){if(e.status===401)fail(e);else{host.appendChild(msg);setMsg(msg,e.message,true)}})}
  function jobWord(st){return ({received:'grabbed, starting',submitted:'grabbed, starting',downloading:'downloading',pulling:'copying from the download client',downloaded:'downloaded',queued_transcode:'waiting to transcode',transcoding:'transcoding',verifying:'checking',ready:'placing in the folder'})[st]||('processing ('+st+')')}
  function aModal(title,width){var ov=el('div','arr a-overlay');var m=el('div','a-modal');m.setAttribute('role','dialog');m.setAttribute('aria-label',title);if(width)m.style.maxWidth=width;
    var h=el('div','a-modal-h');h.appendChild(el('span','',title));var x=aIcon('x','close',function(){ov.remove()});h.appendChild(x);m.appendChild(h);var b=el('div','a-modal-b');m.appendChild(b);var f=el('div','a-modal-f');m.appendChild(f);ov.appendChild(m);
    ov.onclick=function(e){if(e.target===ov)ov.remove()};document.body.appendChild(ov);return {ov:ov,body:b,foot:f,close:function(){ov.remove()}}}
  function aSearchModal(s,ep){var md=aModal('Interactive search · '+s.name+(ep?' · episode '+ep:''));
    var bar=el('div','a-row');var q=el('input','a-input');q.type='search';q.placeholder=s.name;q.setAttribute('aria-label','search nyaa');var go=el('button','a-btn primary','Search');go.type='button';bar.appendChild(q);bar.appendChild(go);md.body.appendChild(bar);
    md.body.appendChild(el('div','a-dim a-small','Searches nyaa (English-translated anime) for the show'+(ep?'; releases numbered '+ep+' come first, but a later season may number it differently':'')+'. A warning says why a release would not be picked by itself; you can still grab it.'));
    var res=el('div','a-tw');md.body.appendChild(res);var msg=aMsg();md.body.appendChild(msg);var close=el('button','a-btn','Close');close.type='button';close.onclick=md.close;md.foot.appendChild(close);
    function run(){res.replaceChildren(el('div','a-dim','Searching…'));api('GET','/shows/search?folder='+encodeURIComponent(s.folder)+'&q='+encodeURIComponent(q.value)+'&episode='+ep).then(function(list){res.replaceChildren();
        if(!list.length){res.appendChild(el('div','a-empty','nyaa has nothing for "'+(q.value||s.name)+'". Try a shorter or different title, e.g. the English one.'));return}
        var t=el('table','a-t'),hr=el('tr');['Age','Title','Group','Quality','Size','Peers','',''].forEach(function(h){hr.appendChild(el('th','',h))});var th=el('thead');th.appendChild(hr);t.appendChild(th);var tb=el('tbody');
        list.forEach(function(r){var tr=el('tr');tr.appendChild(el('td','a-dim',r.published&&r.published[0]!=='0'?ago(r.published):''));tr.appendChild(el('td','a-mono',r.title));var g=el('td');if(r.group)g.appendChild(aLbl(r.group));tr.appendChild(g);var qq=el('td');if(r.res)qq.appendChild(aLbl(r.res,s.quality&&s.quality!=='any'&&r.res!==s.quality?'orange':''));tr.appendChild(qq);
          tr.appendChild(el('td','a-dim',aSize(r.size)));tr.appendChild(el('td','a-dim',String(r.seeders)));var wc=el('td');if(r.warning){var wi=el('span','a-rej');wi.title=r.warning;wi.appendChild(svg('warn',r.warning));wc.appendChild(wi)}tr.appendChild(wc);
          var dc=el('td');var b=el('button','a-dl');b.type='button';b.setAttribute('aria-label','grab '+r.title);b.title=r.warning?'Grab anyway: '+r.warning:'Grab';b.appendChild(svg('dl'));
          b.onclick=function(){b.disabled=true;api('POST','/shows/grab',{folder:s.folder,title:r.title}).then(function(){b.replaceChildren(svg('dl'));b.classList.add('done');setMsg(msg,'Started '+r.title+'. It goes through the usual downloads and lands in the folder.')}).catch(function(e){b.disabled=false;setMsg(msg,e.message,true)})};dc.appendChild(b);tr.appendChild(dc);tb.appendChild(tr)});
        t.appendChild(tb);res.appendChild(t)}).catch(function(e){res.replaceChildren();setMsg(msg,e.message,true)})}
    go.onclick=run;q.onkeydown=function(e){if(e.key==='Enter')run()};run()}
  function aOrder(list){var box=el('div','a-order');var items=list.slice();
    function draw(){box.replaceChildren();items.forEach(function(g,i){var it=el('div','a-it');it.appendChild(el('b','',g));if(i===0)it.appendChild(el('span','a-dim a-small','first choice'));
      var up=aIcon('up','move '+g+' up',function(){if(i>0){items.splice(i-1,0,items.splice(i,1)[0]);draw()}});var dn=aIcon('down','move '+g+' down',function(){if(i<items.length-1){items.splice(i+1,0,items.splice(i,1)[0]);draw()}});var rm=aIcon('x','remove '+g,function(){items.splice(i,1);draw()});
      it.appendChild(up);it.appendChild(dn);it.appendChild(rm);box.appendChild(it)});
      var add=el('div','a-row');var inp=el('input','a-input');inp.placeholder='add a group, e.g. Judas';inp.setAttribute('aria-label','add a group');var ab=el('button','a-btn','Add');ab.type='button';ab.onclick=function(){var v=inp.value.trim();if(v&&items.map(function(x){return x.toLowerCase()}).indexOf(v.toLowerCase())<0){items.push(v);draw()}};inp.onkeydown=function(e){if(e.key==='Enter'){e.preventDefault();ab.onclick()}};add.appendChild(inp);add.appendChild(ab);box.appendChild(add)}
    draw();return {el:box,value:function(){return items.slice()}}}
  function aForm(){var f=el('div','a-form');return {el:f,row:function(label,ctl,help,id){var l=el('label','a-k',label);if(id)l.htmlFor=id;f.appendChild(l);var c=el('div');c.appendChild(ctl);if(help)c.appendChild(el('div','a-help',help));f.appendChild(c)}}}
  function aEditModal(s,after,defaults){var md=aModal('Edit · '+s.name,'860px');var f=aForm();md.body.appendChild(f.el);
    var fol=el('label','a-check');var cb=el('input');cb.type='checkbox';cb.checked=!!s.followed;fol.appendChild(cb);fol.appendChild(el('span','','Grab new episodes by themselves'));f.row('Following',fol);
    var gl=el('label','a-check');var gc=el('input');gc.type='checkbox';gc.checked=s.followed?!!s.fillGaps:true;gl.appendChild(gc);gl.appendChild(el('span','','Also fill gaps (episodes missing between the ones in the folder), once a day'));f.row('Gaps',gl);
    var order=aOrder(s.groups&&s.groups.length?s.groups:(defaults||['SubsPlease']));f.row('Groups, in order',order.el,'The first group that releases an episode gets it. A later group is used only when an earlier one skipped that episode (it released a later one), or after a day.');
    var q=el('select','a-input');q.id='aq';['1080p','720p','480p','2160p','any'].forEach(function(v){var o=new Option(v==='any'?'any resolution':v,v);o.selected=(s.quality||'1080p')===v;q.appendChild(o)});f.row('Quality',q,null,'aq');
    var ti=el('textarea','a-input a-area');ti.id='at';ti.value=(s.titles||[]).join('\n');f.row('Other titles',ti,'One per line. Releases under any of these titles belong to this folder, for following and for the importer. The folder name always counts.','at');
    var fe=el('input','a-input');fe.id='af';fe.type='number';fe.min='0';fe.value=String(s.fromEpisode||0);f.row('Grab episodes above',fe,'Following starts from the newest episode on disk. Lower it to grab older ones too; gaps are separate (Search gaps).','af');
    var ai=el('input','a-input');ai.id='aa';ai.type='number';ai.min='0';ai.value=String(s.anilistId||0);f.row('AniList id',ai,'Only for the status shown (airing, episodes, next episode). 0 finds it by the folder name. The number is in the AniList address.','aa');
    var msg=aMsg();md.body.appendChild(msg);var cancel=el('button','a-btn','Cancel');cancel.type='button';cancel.onclick=md.close;var save=el('button','a-btn primary','Save');save.type='button';
    save.onclick=function(){save.disabled=true;api('PUT','/shows/one',{folder:s.folder,followed:cb.checked,groups:order.value(),quality:q.value,titles:ti.value.split(/\n|,/),fromEpisode:Number(fe.value)||0,anilistId:Number(ai.value)||0,fillGaps:gc.checked}).then(function(){md.close();after()}).catch(function(e){save.disabled=false;setMsg(msg,e.message,true)})};
    md.foot.appendChild(cancel);md.foot.appendChild(save)}
  // Naming: folders whose files do not share one name form. New downloads are
  // named after their folder already; these are the older files.
  function aStyleSample(name,st){var ep=st.marked?'S01E'+('0'+4).slice(-Math.max(st.pad,2)):('000'+4).slice(-st.pad);return name+' - '+ep+(st.res?' (1080p)':st.tags?' [Group 1080p]':'')+'.mkv'}
  function aNamingPage(w){
    var msg=aMsg();var host=el('div','a-content');
    w.appendChild(aToolbar([aBtn('back','Library',function(){aView.page='list';app()}),'|',aBtn('refresh','Check again',function(){app()})]));
    w.appendChild(host);host.appendChild(el('p','a-small','New downloads are named after the show folder they land in, in the way that folder already names its files. Below are folders whose older files do not fit: rename them after a preview, replace files whose names give no episode number, and pick one copy where an episode is kept twice. Nothing changes until you press Rename.'));host.appendChild(msg);
    setMsg(msg,'Reading every show folder…');
    api('GET','/shows/naming').then(function(d){msg.textContent='';var list=d.folders||[];
      if(!list.length){host.appendChild(el('div','a-empty','Every show folder fits its own naming.'));return}
      var t=el('table','a-t'),hr=el('tr');['Show','To rename','No episode number','Kept twice',''].forEach(function(h){hr.appendChild(el('th','',h))});var th=el('thead');th.appendChild(hr);t.appendChild(th);var tb=el('tbody');t.appendChild(tb);
      list.forEach(function(f){var tr=el('tr');var c=el('td');var a=el('button','a-title',f.name);a.type='button';a.onclick=function(){aView.page='show';aView.folder=f.folder;app()};c.appendChild(a);tr.appendChild(c);
        tr.appendChild(el('td','',f.renames?String(f.renames):'–'));var u=el('td');if(f.unreadable)u.appendChild(aLbl(f.unreadable+' to replace','warn'));else u.textContent='–';tr.appendChild(u);
        var dd=el('td');if(f.duplicates)dd.appendChild(aLbl(f.duplicates+' twice','warn'));else dd.textContent='–';tr.appendChild(dd);
        var ac=el('td');var b=el('button','a-btn','Review');b.type='button';b.onclick=function(){aTidyModal(f.folder,f.name,function(){app()})};ac.appendChild(b);tr.appendChild(ac);tb.appendChild(tr)});
      host.appendChild(t);host.appendChild(el('p','a-small',list.length+' folder'+(list.length===1?'':'s')+' to look at.'))}).catch(function(e){setMsg(msg,e.message,true)})}
  function aTidyModal(folder,name,after){var md=aModal('Tidy names · '+name,'900px');var m=aMsg();md.body.appendChild(m);setMsg(m,'Reading the folder…');
    api('GET','/shows/tidy?folder='+encodeURIComponent(folder)).then(function(p){m.textContent='';
      md.body.appendChild(el('p','a-small','This folder names its episodes like: '));md.body.appendChild(el('pre','a-mono',aStyleSample(name,p.style)));
      var picks=[];
      if(p.renames.length){md.body.appendChild(el('h4','a-h',p.renames.length+' to rename'));var t=el('table','a-t'),tb=el('tbody');
        var all=el('input');all.type='checkbox';all.checked=true;all.setAttribute('aria-label','all renames');var hr=el('tr');var c0=el('th');c0.appendChild(all);hr.appendChild(c0);['Now','Becomes'].forEach(function(h){hr.appendChild(el('th','',h))});var th=el('thead');th.appendChild(hr);t.appendChild(th);
        p.renames.forEach(function(r){var tr=el('tr');var cb=el('input');cb.type='checkbox';cb.checked=true;cb.setAttribute('aria-label','rename '+r.from);picks.push([cb,r]);var c=el('td');c.appendChild(cb);tr.appendChild(c);tr.appendChild(el('td','a-mono',r.from));tr.appendChild(el('td','a-mono',r.to));tb.appendChild(tr)});
        all.onchange=function(){picks.forEach(function(x){x[0].checked=all.checked})};t.appendChild(tb);md.body.appendChild(t)}
      if(p.unreadable.length){md.body.appendChild(el('h4','a-h',p.unreadable.length+' with no episode number'));md.body.appendChild(el('p','a-small','The importer cannot tell which episode these are. Replace them with a release that names its episode, then delete the old file.'));
        var ul=el('div','a-list');p.unreadable.forEach(function(f){var r=el('div','a-row');r.appendChild(el('span','a-mono',f));var b=el('button','a-btn','Search nyaa');b.type='button';b.onclick=function(){aSearchModal({folder:folder,name:name},0)};r.appendChild(b);ul.appendChild(r)});md.body.appendChild(ul)}
      if(p.duplicates.length){md.body.appendChild(el('h4','a-h',p.duplicates.length+' kept twice'));md.body.appendChild(el('p','a-small','The same episode is in the folder more than once. Keep one copy and delete the others yourself.'));
        var dl=el('div','a-list');p.duplicates.forEach(function(g){var r=el('div','a-dup');g.forEach(function(f){r.appendChild(el('div','a-mono',f))});dl.appendChild(r)});md.body.appendChild(dl)}
      if(!p.renames.length&&!p.unreadable.length&&!p.duplicates.length)md.body.appendChild(el('div','a-empty','Every file here fits the folder\'s naming.'));
      if(p.renames.length){md.foot.appendChild(el('span','a-small','Emby and Jellyfin may see renamed files as new: their watched state can reset.'));
        var go=el('button','a-btn primary','Rename');go.type='button';go.onclick=function(){var chosen=picks.filter(function(x){return x[0].checked}).map(function(x){return x[1]});if(!chosen.length){setMsg(m,'Nothing is ticked.',true);return}
          go.disabled=true;api('POST','/shows/tidy',{folder:folder,renames:chosen}).then(function(r){setMsg(m,'Renamed '+r.renamed+(r.skipped&&r.skipped.length?'; skipped: '+r.skipped.join('; '):'.'),r.skipped&&r.skipped.length);md.body.scrollTop=0;go.remove();if(after)after()}).catch(function(e){go.disabled=false;setMsg(m,e.message,true)})};md.foot.appendChild(go)}
    }).catch(function(e){setMsg(m,e.message,true)})}
  function aAddPage(w){
    w.appendChild(aToolbar([aBtn('back','Library',function(){aView.page='list';app()}),'>']));var c=el('div','a-content');w.appendChild(c);
    var bar=el('label','a-search big');bar.appendChild(svg('search'));var q=el('input');q.type='search';q.placeholder='Search for a show by any title';q.setAttribute('aria-label','search for a show');bar.appendChild(q);c.appendChild(bar);
    c.appendChild(el('div','a-dim a-small','Searches what nyaa has released, by any title. A show is its folder: nothing is looked up to place files.'));
    var res=el('div','a-cards');c.appendChild(res);var msg=aMsg();c.appendChild(msg);
    function run(){if(!q.value.trim())return;res.replaceChildren(el('div','a-dim','Searching…'));api('GET','/shows/discover?q='+encodeURIComponent(q.value)).then(function(d){res.replaceChildren();
        if(!d.results.length){res.appendChild(el('div','a-empty','Nothing found on nyaa.'));return}
        d.results.forEach(function(x){var card=el('div','a-card');var body=el('div','a-card-b');body.appendChild(el('h3','',x.title));
          body.appendChild(el('div','a-dim',x.releases+' release'+(x.releases>1?'s':'')+' · newest episode '+x.latest+(x.newest?' · '+ago(x.newest)+' ago':'')));
          var ch=el('div','a-chips');x.groups.forEach(function(g){ch.appendChild(aLbl(g))});x.res.forEach(function(r){ch.appendChild(aLbl(r))});body.appendChild(ch);card.appendChild(body);
          if(x.folder){body.appendChild(el('div','a-small a-ok','Already in the library: '+x.folder));var o=el('button','a-btn','Open');o.type='button';o.onclick=function(){aView.page='show';aView.folder=x.folder;app()};card.appendChild(o)}
          else{var a=el('button','a-btn primary','Add');a.type='button';a.onclick=function(){addForm(x,d.roots,card)};card.appendChild(a)}
          res.appendChild(card)})}).catch(function(e){res.replaceChildren();setMsg(msg,e.message,true)})}
    q.onkeydown=function(e){if(e.key==='Enter')run()};q.focus();
    function addForm(x,roots,after){var old=c.querySelector('.a-addform');if(old)old.remove();var box=el('div','a-modal a-addform');var h=el('div','a-modal-h');h.appendChild(el('span','','Add · '+x.title));box.appendChild(h);var b=el('div','a-modal-b');box.appendChild(b);var f=aForm();b.appendChild(f.el);
      var rs=el('select','a-input');rs.id='ar';roots.forEach(function(r){rs.appendChild(new Option(r.dir+' ('+r.category+')',r.dir))});if(roots.length>1)f.row('Library',rs,null,'ar');
      var nm=el('input','a-input');nm.id='an';nm.value=x.title;f.row('Folder name',nm,'A new folder in the library, or the name of one you already have.','an');
      var gs=x.groups.slice().sort(function(a,b){return (b==='SubsPlease')-(a==='SubsPlease')});var order=aOrder(gs.length?gs:['SubsPlease']);f.row('Groups, in order',order.el,'Remove the groups you do not want.');
      var qs=el('select','a-input');qs.id='aqq';['1080p','720p','480p','any'].forEach(function(v){qs.appendChild(new Option(v==='any'?'any resolution':v,v))});f.row('Quality',qs,null,'aqq');
      var start=el('select','a-input');start.id='as';start.appendChild(new Option('every episode released so far ('+x.latest+')','all'));start.appendChild(new Option('new episodes only','new'));f.row('Start with',start,null,'as');
      var m=aMsg();b.appendChild(m);var ft=el('div','a-modal-f');var cn=el('button','a-btn','Cancel');cn.type='button';cn.onclick=function(){box.remove()};var ok=el('button','a-btn primary','Add and follow');ok.type='button';
      ok.onclick=function(){ok.disabled=true;setMsg(m,'Adding and checking nyaa…');api('POST','/shows/add',{root:rs.value,name:nm.value,groups:order.value(),quality:qs.value,titles:nm.value.trim()===x.title?[]:[x.title],fromStart:start.value==='all',fromEpisode:start.value==='all'?0:x.latest,fillGaps:true}).then(function(r){
        setMsg(m,(r.warning?r.warning+' ':'')+(r.started.length?'Started '+r.started.length+' download'+(r.started.length>1?'s':'')+'.':'Following; nothing to grab yet.'));setTimeout(function(){aView.page='show';aView.folder=r.show.folder;app()},1200)}).catch(function(e){ok.disabled=false;setMsg(m,e.message,true)})};
      ft.appendChild(cn);ft.appendChild(ok);box.appendChild(ft);after.after(box);nm.focus()}
  }
  // ---- Indexers (roadmap D1): arr-style, backend first; design pass later ----
  var iView={q:'',type:'search',cat:''};
  // Missing: what each arr reports as wanted and not on disk, one section per
  // arr category, as posters (album covers for music) taken from the arr.
  var missQ='',missAll={};
  var ARRNAME={sonarr:['Sonarr','var(--tv)'],radarr:['Radarr','var(--movie)'],lidarr:['Lidarr','var(--music)']};
  function missingView(body){
    var top=el('div','hv-pagehead');body.appendChild(top);var host=el('div','hv-col');body.appendChild(host);
    api('GET','/missing').then(function(d){
      var arrs=d.arrs||[],total=arrs.reduce(function(n,a){return n+a.count},0);
      top.appendChild(el('h2','',total+' missing'));top.appendChild(el('span','hv-sp'));
      var q=el('input');q.type='search';q.placeholder='Filter';q.setAttribute('aria-label','filter missing');q.value=missQ;top.appendChild(q);
      top.appendChild(el('span','hv-note',d.updatedAt&&d.updatedAt[0]!=='0'?'From the arrs · checked '+ago(d.updatedAt)+' ago':'From the arrs'));
      var errs=d.errors||[];if(errs.length){var eb=hvBox('Problems reading the arrs');var l=el('div','hv-list');errs.slice(0,10).forEach(function(e){var r=el('div','hv-item slim');r.appendChild(hvPill('Error','var(--bad)'));var t=el('div','hv-it');t.appendChild(el('span','',e));r.appendChild(t);l.appendChild(r)});eb.appendChild(l);host.appendChild(eb)}
      if(!arrs.length){host.appendChild(el('div','hv-empty','No Sonarr, Radarr or Lidarr categories yet. Add them under Settings › Arrs.'));return}
      var secs=[];
      arrs.forEach(function(a){var an=ARRNAME[a.kind]||[a.kind,'var(--dim)'];var box=el('section','hv-box hv-miss');box.style.setProperty('--lc',an[1]);
        var h=el('div','hv-miss-h');h.appendChild(hvPill(an[0],an[1],true));h.appendChild(el('h3','',a.category));var cnt=el('span','hv-note');h.appendChild(cnt);h.appendChild(el('span','hv-sp'));
        if(a.url){var o=el('a','hv-open','Open '+an[0]);o.href=a.url+(a.kind==='lidarr'?'/wanted/missing':'/wanted/missing');o.target='_blank';o.rel='noopener';h.appendChild(o)}
        box.appendChild(h);var grid=el('div','hv-pgrid'+(a.kind==='lidarr'?' sq':''));box.appendChild(grid);var more=el('button','hv-btn sm hv-more');more.type='button';box.appendChild(more);host.appendChild(box);secs.push({a:a,grid:grid,cnt:cnt,more:more,box:box})});
      function card(g,kind){var c=el(g.link?'a':'div','hv-pc');if(g.link){c.href=g.link;c.target='_blank';c.rel='noopener';c.title='Open in the arr'}
        var p=hvPoster(g.poster,g.title);if(kind==='lidarr')p.classList.add('sq');c.appendChild(p);
        if(g.kind==='episode'){var b=el('span','hv-badge',String(g.items.length));b.title=g.items.length+' episode'+(g.items.length===1?'':'s')+' missing';p.appendChild(b)}
        c.appendChild(el('b','',g.title));
        var sub=g.kind==='episode'?g.items.slice(0,3).map(function(x){return x.detail}).join(', ')+(g.items.length>3?' +'+(g.items.length-3):''):(g.sub||'');
        var sp=el('span','',sub);if(g.kind==='episode'&&g.items.length>3)sp.title=g.items.map(function(x){return x.detail}).join(', ');c.appendChild(sp);return c}
      function draw(){var ql=missQ.toLowerCase();secs.forEach(function(x){var gs=x.a.groups.filter(function(g){return !ql||(g.title+' '+(g.sub||'')).toLowerCase().indexOf(ql)>=0});
        x.grid.replaceChildren();var lim=gs.length;
        gs.slice(0,lim).forEach(function(g){x.grid.appendChild(card(g,x.a.kind))});
        x.cnt.textContent=x.a.kind==='sonarr'?x.a.count+' episodes in '+x.a.groups.length+' series':x.a.kind==='lidarr'?x.a.count+' albums':x.a.count+' movies';
        if(!gs.length)x.grid.appendChild(el('div','hv-empty',ql?'Nothing matches.':'Nothing missing.'));
        x.more.hidden=gs.length<=lim;x.more.textContent='Show all '+gs.length;x.more.onclick=function(){missAll[x.a.category]=true;draw()}})}
      q.oninput=function(){missQ=q.value;draw()};draw();
    }).catch(fail)}
  // Clients: everything being downloaded, in TorBox, the built-in torrent
  // client and the built-in usenet downloader, with their controls.
  function clientsView(body,only){
    var WHERE={torbox:'TorBox',usenetq:'Usenet',torrents:'Torrent'}[only];
    function rate(b){b=b||0;return b>=1048576?(b/1048576).toFixed(1)+' MB/s':b>=1024?Math.round(b/1024)+' KB/s':b+' B/s'}
    function size(b){b=b||0;return b>=1073741824?(b/1073741824).toFixed(2)+' GB':b>=1048576?Math.round(b/1048576)+' MB':Math.round(b/1024)+' KB'}
    function eta(s){if(!s)return '';if(s<60)return s+'s';if(s<3600)return Math.round(s/60)+'m';return Math.floor(s/3600)+'h '+Math.round(s%3600/60)+'m'}
    var colors={TorBox:'var(--accent)',Torrent:'var(--tv)',Usenet:'var(--movie)',qBittorrent:'var(--tv)',NZBGet:'var(--movie)'};
    var labels={pause:['Pause','x'],resume:['Resume','refresh'],top:['Move to top','up'],builtin:['Download here instead','dl'],remove:['Remove','x'],retry:['Retry','refresh']};
    var first=true,tops,list,hist,addBox,limBox,msg=el('span','msg'),open={},logs={},lastData=null;
    function hms(t){var d=new Date(t);return isNaN(d)?'':d.toLocaleString([], {month:'short',day:'numeric',hour:'2-digit',minute:'2-digit',second:'2-digit'})}
    function fetchLog(id){return api('GET','/clients/'+encodeURIComponent(id)+'/log').then(function(x){logs[id]=x}).catch(function(e){logs[id]={error:e.message}})}
    function details(x){var p=el('div','hv-details');var L=logs[x.id];
      if(!L){p.appendChild(el('div','hv-note','loading…'));return p}
      if(L.error){p.appendChild(el('div','err',L.error));return p}
      var facts=[(L.where||'not sent yet'),L.state];if(L.attempts)facts.push('try '+(L.attempts+1));if(L.nextRun&&L.nextRun[0]!=='0')facts.push('next try '+hms(L.nextRun));
      p.appendChild(el('div','hv-note',facts.join(' · ')));
      if(L.reason)p.appendChild(el('div','hv-note',L.reason));
      if(L.folder)p.appendChild(el('div','hv-note','folder: '+L.folder));
      if(L.files&&L.files.length)p.appendChild(el('div','hv-note','files: '+L.files.join(', ')));
      var pre=el('div','hv-log');(L.lines||[]).forEach(function(l){var r=el('div',l.from==='client'?'c':'p');r.appendChild(el('span','t',hms(l.at)));r.appendChild(document.createTextNode(l.msg));pre.appendChild(r)});
      p.appendChild(pre);setTimeout(function(){pre.scrollTop=pre.scrollHeight},0);return p}
    function act(id,a,name,b){if(a==='remove'&&!confirm('Remove '+name+'? Its files are deleted.'))return;b.disabled=true;
      api('POST','/clients/'+encodeURIComponent(id)+'/'+a).then(load).catch(function(e){b.disabled=false;alertMsg(e.message)})}
    // The client tabs: a table in the style of each client's own app.
    var COLS={TorBox:['Name','At TorBox','Size',''],Usenet:['#','Name','Progress','Size','Time left',''],Torrent:['Name','Progress','Status','Seeds','Peers','Down','Up','Time left','']};
    function bar(x,color){var w=el('div','hv-tbar');var b=el('div','hv-bar');var f=el('span');f.style.width=Math.round((x.progress||0)*100)+'%';f.style.background=color;b.appendChild(f);w.appendChild(el('span','',Math.round((x.progress||0)*100)+'%'));w.appendChild(b);return w}
    function table(rows,past){var wrap=el('div','hv-twrap');var t=el('table','hv-table');var hr=el('tr');
      (past?['Name','Finished','']:COLS[WHERE]).forEach(function(h){var th=el('th','',h);th.scope='col';hr.appendChild(th)});var th0=el('thead');th0.appendChild(hr);t.appendChild(th0);var tb=el('tbody');t.appendChild(tb);
      rows.forEach(function(x){var r=el('tr',past&&x.state==='failed'?'bad':x.paused?'dim':'');function td(c,cls){var d=el('td',cls||'');if(typeof c==='string')d.textContent=c;else if(c)d.appendChild(c);r.appendChild(d);return d}
        var nm=el('div','hv-tname');nm.appendChild(el('b','',x.name));var sub=[x.category];if(x.note&&(!past||x.state==='failed'))sub.push(x.note);if(!past&&x.stage&&WHERE!=='TorBox')sub.unshift(x.paused?'paused':x.stage);nm.appendChild(el('span','',sub.filter(Boolean).join(' · ')));
        if(past){td(nm);td(({cleaned:'finished',imported:'imported',ready:'ready',failed:'failed'}[x.state]||x.state)+' '+ago(x.when)+' ago',x.state==='failed'?'bad':'')}
        else if(WHERE==='TorBox'){td(nm);var at=el('div');at.appendChild(el('span','',x.paused?'paused':x.stage||'')) ;at.appendChild(bar(x,'var(--st-down)'));td(at);td(x.size?size(x.size):'')}
        else if(WHERE==='Usenet'){td(x.position?String(x.position):'—','hv-tnum');td(nm);td(bar(x,x.stage==='downloading'||!x.stage?'var(--st-down)':'var(--warn)'));td(x.size?size(x.size):'');td(eta(x.eta)||'—')}
        else{td(nm);td(bar(x,'var(--st-down)'));td(x.paused?'Paused':x.stage||'Downloading');td(String(x.seeds||0));td(String(x.peers||0));td(x.down?rate(x.down):'—');td(x.up?rate(x.up):'—');td(eta(x.eta)||'—')}
        var a=el('div','hv-acts');a.appendChild(hvBtn(open[x.id]?'Hide':'Details','',function(){if(open[x.id]){delete open[x.id];draw(lastData)}else{open[x.id]=true;draw(lastData);fetchLog(x.id).then(function(){draw(lastData)})}}));
        (x.can||[]).forEach(function(k){var L=labels[k];if(L){var b=hvBtn(L[0],'',function(bb){act(x.id,k,x.name,bb)});if(k==='remove')b.classList.add('bad');a.appendChild(b)}});[].forEach.call(a.children,function(b){b.classList.add('sm')});td(a,'hv-tacts');tb.appendChild(r);
        if(open[x.id]){var dr=el('tr','hv-tdet');var dd=el('td');dd.colSpan=(past?3:COLS[WHERE].length);dd.appendChild(details(x));dr.appendChild(dd);tb.appendChild(dr)}});
      wrap.appendChild(t);return wrap}
    function item(x,past){var r=el('div','hv-item');r.appendChild(hvPill(x.where||'Waiting',colors[x.where]||'',!!x.where));
      var t=el('div','hv-it');t.appendChild(el('b','',x.name));var bits=[];
      if(!past){bits.push(Math.round((x.progress||0)*100)+'%'+(x.size?' of '+size(x.size):''));
        if(x.paused)bits.push('paused');else if(x.stage)bits.push(x.stage);
        if(x.down)bits.push('↓ '+rate(x.down));if(x.up)bits.push('↑ '+rate(x.up));
        if(x.where==='Torrent'&&(x.peers||x.seeds))bits.push(x.peers+' peers, '+x.seeds+' seeds');
        if(x.position)bits.push('#'+x.position+' in queue');
        if(x.fromBackup)bits.push(x.fromBackup+' articles from backups');
        if(x.eta)bits.push(eta(x.eta)+' left')}
      else bits.push(({cleaned:'finished',imported:'imported',ready:'ready',failed:'failed'}[x.state]||x.state)+' '+ago(x.when)+' ago');
      bits.push(x.category);t.appendChild(el('span','hv-note',bits.join(' · ')));
      if(x.note&&(past?x.state==='failed':true))t.appendChild(el('div',past?'err':'hv-note',x.note));
      if(!past){var bar=el('div','hv-bar');var fill=el('span');fill.style.width=Math.round((x.progress||0)*100)+'%';fill.style.background=colors[x.where]||'var(--dim)';bar.appendChild(fill);t.appendChild(bar)}
      r.appendChild(t);var acts=el('div','hv-acts');
      acts.appendChild(hvBtn(open[x.id]?'Hide details':'Details','list',function(){if(open[x.id]){delete open[x.id];draw(lastData)}else{open[x.id]=true;draw(lastData);fetchLog(x.id).then(function(){draw(lastData)})}}));
      (x.can||[]).forEach(function(a){var L=labels[a];if(L)acts.appendChild(hvBtn(L[0],L[1],function(b){act(x.id,a,x.name,b)}))});r.appendChild(acts);
      if(open[x.id]){var wrap=el('div');wrap.appendChild(r);wrap.appendChild(details(x));return wrap}
      return r}
    function draw(d){
      if(!d)return;lastData=d;
      if(only==='add')d=Object.assign({},d,{items:d.items.filter(function(x){return x.source==='manual'}),history:d.history.filter(function(x){return x.source==='manual'})});
      if(WHERE){d=Object.assign({},d,{items:d.items.filter(function(x){return x.where===WHERE}),history:d.history.filter(function(x){return x.where===WHERE})});
        if(WHERE==='Usenet')d.items.sort(function(a,b){return (a.position||0)-(b.position||0)})}
      tops.replaceChildren();
      function card(name,color,lines){var c=el('div','hv-box');c.style.flex='1';c.style.minWidth='180px';var h=el('h3','',name);h.style.color=color;c.appendChild(h);lines.forEach(function(l){c.appendChild(el('div','hv-note',l))});tops.appendChild(c)}
      function stat(label,value,tone){var c=el('div','hv-stat'+(tone?' '+tone:''));c.appendChild(el('span','',label));c.appendChild(el('b','',value));tops.appendChild(c)}
      if(WHERE==='TorBox'){if(!d.torbox.set){stat('TorBox','not connected (Settings › TorBox)','warn')}else{stat('At TorBox',String(d.torbox.active||0));
        if(sys&&sys.torbox){stat('Usenet adds this hour',(sys.torbox.used||0)+' of '+sys.torbox.limit);stat('Torrent adds this hour',(sys.torbox.torrentUsed||0)+' of '+sys.torbox.limit);if(sys.torbox.backoffUntil)stat('New adds','paused by TorBox','warn')}}}
      else if(WHERE==='Usenet'){if(!d.builtin.usenet)stat('Usenet','no provider (Settings › Usenet providers)','warn');else{stat('Speed',rate(d.usenet.speed),'cyan');stat('In the queue',(d.usenet.active?1:0)+' downloading · '+(d.usenet.queued||0)+' waiting');
        (d.usenet.servers||[]).forEach(function(sv){stat(sv.name,sv.problem?sv.problem:(sv.conns!=null?sv.conns+' connections':'ready'),sv.problem?'bad':'')})}}
      else if(WHERE==='Torrent'){if(!d.builtin.torrent)stat('Torrents','off (Settings › VPN and torrents)','warn');else{stat('VPN',d.torrent.running?'up'+(d.torrent.port?' · port '+d.torrent.port:' · no forwarded port'):'waiting for the VPN',d.torrent.running?'':'warn');stat('Down',rate(d.torrent.downRate),'cyan');stat('Up (capped)',rate(d.torrent.upRate));stat('Active',String(d.torrent.active||0))}}
      else{card('TorBox','var(--accent)',[d.torbox.set?d.torbox.active+' at TorBox':'not connected']);
      card('Torrent','var(--tv)',d.builtin.torrent?['↓ '+rate(d.torrent.downRate)+'  ↑ '+rate(d.torrent.upRate),(d.torrent.active||0)+' active · '+(d.torrent.running?'VPN up':'waiting for the VPN')]:['off']);
      card('Usenet','var(--movie)',d.builtin.usenet?['↓ '+rate(d.usenet.speed),(d.usenet.active?1:0)+' active · '+(d.usenet.queued||0)+' waiting']:['no provider'])}
      list.replaceChildren();
      if(!d.items.length)list.appendChild(el('div','hv-empty','Nothing downloading right now.'));
      else if(WHERE)list.appendChild(table(d.items,false));
      else d.items.forEach(function(x){list.appendChild(item(x,false))});
      hist.replaceChildren();
      if(!d.history.length)hist.appendChild(el('div','hv-empty','Nothing in the last three days.'));
      else if(WHERE)hist.appendChild(table(d.history,true));
      else d.history.forEach(function(x){hist.appendChild(item(x,true))});
      if(first){first=false;if(addBox)buildAdd(d);if(limBox)buildLimits(d)}
    }
    function buildAdd(d){addBox.replaceChildren();addBox.appendChild(el('h3','','Add a download'));
      var c=el('div','card');var mg=row(c,'Magnet link',{ph:'magnet:?xt=urn:btih:…'});
      var fw=el('div','fld');fw.appendChild(el('label','','or a .torrent or .nzb file'));var fi=el('input');fi.type='file';fi.accept='.torrent,.nzb';fw.appendChild(fi);c.appendChild(fw);
      var cw=el('div','fld');cw.appendChild(el('label','','Category'));var cs=el('select');(d.categories||[]).forEach(function(n){cs.appendChild(new Option(n+((d.plain||[]).indexOf(n)>=0?' (plain files)':''),n))});
      if((d.plain||[]).length)cs.value=d.plain[0];cw.appendChild(cs);
      var ch=el('div','fh','');cw.appendChild(ch);function hint(){ch.textContent=(d.plain||[]).indexOf(cs.value)>=0?'Plain files: everything is kept as released (software included), with no malicious-file check and no Tdarr.':(d.plain||[]).length?'A media category: only video, audio and subtitles are kept, and programs are blocked. Use a plain-files category for software.':'A media category: only video, audio and subtitles are kept, and programs are blocked. For software, add a “plain files” category under Settings › Arrs.'}cs.onchange=hint;hint();c.appendChild(cw);
      var ww=el('div','fld');ww.appendChild(el('label','','Download with'));var wsel=el('select');[['','The usual order (TorBox first)'],['torbox','TorBox only'],['builtin','HyveMynd’s own client']].forEach(function(o){wsel.appendChild(new Option(o[1],o[0]))});ww.appendChild(wsel);c.appendChild(ww);
      var go=el('button','go','Add');var am=el('span','msg');go.onclick=function(){var fd=new FormData();fd.append('magnet',mg.value);if(fi.files[0])fd.append('file',fi.files[0]);fd.append('category',cs.value);fd.append('prefer',wsel.value);
        am.className='msg';am.textContent='adding…';go.disabled=true;
        fetch('/admin/api/clients/add',{method:'POST',headers:{'X-HyveMynd-Request':'1'},body:fd,credentials:'same-origin'}).then(function(r){return r.json().then(function(x){if(!r.ok)throw new Error(x.error||('HTTP '+r.status));return x})})
          .then(function(x){go.disabled=false;am.className='msg ok';am.textContent='added: '+x.name;mg.value='';fi.value='';load()}).catch(function(e){go.disabled=false;am.className='msg err';am.textContent=e.message})};
      c.appendChild(go);c.appendChild(am);addBox.appendChild(c)}
    function buildLimits(d){limBox.replaceChildren();limBox.appendChild(el('h3','','Speed limits'));var sp=d.speed||{};var c=el('div','card');
      var td=row(c,'Torrent download (KB/s)',{type:'number',value:sp.torrentDownKb||0,help:'0 = no limit'});
      var tu=row(c,'Torrent upload while downloading (KB/s)',{type:'number',value:sp.torrentUpKb||1024,help:'Nothing is shared after a download finishes.'});
      var un=row(c,'Usenet download (KB/s)',{type:'number',value:sp.usenetKb||0,help:'0 = no limit'});
      var sb=el('button','go','Save limits');var lm=el('span','msg');sb.onclick=function(){api('PUT','/clients/limits',{torrentDownKb:parseInt(td.value,10)||0,torrentUpKb:parseInt(tu.value,10)||0,usenetKb:parseInt(un.value,10)||0}).then(function(){lm.className='msg ok';lm.textContent='saved'}).catch(function(e){lm.className='msg err';lm.textContent=e.message})};
      c.appendChild(sb);c.appendChild(lm);limBox.appendChild(c)}
    var sys=null;
    function load(){if(WHERE==='TorBox')api('GET','/system').then(function(x){sys=x}).catch(function(){});return api('GET','/clients').then(function(d){var ids=Object.keys(open);return Promise.all(ids.map(fetchLog)).then(function(){draw(d)})}).catch(fail)}
    body.replaceChildren();
    if(only==='add'){addBox=hvBox('');body.appendChild(addBox);tops=el('div');list=el('div');hist=el('div','hv-list');var b3=hvBox('Your recent manual downloads');b3.appendChild(hist);body.appendChild(b3);
      load();timer=setInterval(function(){if(tab==='add')load()},5000);return}
    if(only==='limits'){limBox=hvBox('');body.appendChild(limBox);tops=el('div');list=el('div');hist=el('div');load();return}
    tops=el('div','hv-stats');body.appendChild(tops);
    var b1=hvBox(WHERE==='Usenet'?'Queue':'Downloading');list=el('div','hv-list');b1.appendChild(list);body.appendChild(b1);
    var b2=hvBox('History · last three days');hist=el('div','hv-list');b2.appendChild(hist);body.appendChild(b2);
    load();timer=setInterval(function(){if(tab===only)load()},3000);
  }
  function addView(body){clientsView(body,'add')}
  function limitsView(body){clientsView(body,'limits')}
  // Usenet: HyveMynd's own usenet downloader and the providers it uses.
  function usenetView(body){
    api('GET','/usenet').then(function(d){body.replaceChildren();
      body.appendChild(head('Built-in usenet','HyveMynd downloads NZBs itself from your providers: it fetches every article from your main providers, asks the backups (in order) for any the main ones lack, repairs damaged files with par2, puts back scrambled file names and unpacks RARs. Releases carrying programs or disguised files are blocked and the arr is told to find another.'));
      var st=d.stats||{},box=hvBox('Now');var l=el('div','hv-list');
      function line(k,v){var r=el('div','hv-item');var t=el('div','hv-it');t.appendChild(el('b','',k));r.appendChild(t);r.appendChild(el('span','hv-note',v));l.appendChild(r)}
      function rate(b){return b>=1048576?(b/1048576).toFixed(1)+' MB/s':b>=1024?Math.round(b/1024)+' KB/s':b+' B/s'}
      var stages={downloading:'Downloading',repairing:'Repairing',unpacking:'Unpacking'};
      line('Downloading',st.active?(stages[st.stage]||st.stage)+(st.stage==='downloading'?' · '+Math.round((st.progress||0)*100)+'% · '+rate(st.speed||0):''):'nothing right now');
      line('Waiting',String(st.queued||0));
      (st.servers||[]).forEach(function(x){if(x.problem)l.appendChild(el('div','err',x.name+': '+x.problem))});
      box.appendChild(l);body.appendChild(box);
      var c=el('div','card');c.appendChild(el('h4','','Where NZBs go'));
      var mw=el('div','fld');var md=el('select');
      [['','TorBox first; here when TorBox is busy or not connected (recommended)'],['local','Always here (TorBox not used for usenet)'],['torbox','Always TorBox, never here']].forEach(function(o){md.appendChild(new Option(o[1],o[0]))});md.value=d.mode||'';mw.appendChild(md);
      mw.appendChild(el('div','fh',d.torbox?'Nothing runs here until a provider below is switched on.':'No TorBox token is set, so NZBs come here once a provider is switched on.'));c.appendChild(mw);body.appendChild(c);
      body.appendChild(el('h2','hv-grph','PROVIDERS'));body.appendChild(el('p','hv-note','Every download is spread across the Main providers; one that lacks a piece is covered by the others. Backups are asked only for what all Main providers lack.'));
      var list=el('div','hv-erows');body.appendChild(list);var rows=[];
      var prios=[[0,'Main'],[1,'Backup 1'],[2,'Backup 2'],[3,'Backup 3']];
      function addRow(v){v=v||{on:true,tls:true,port:563,connections:20,priority:0};
        var wrapR=el('div','hv-erow');var top=el('div','hv-erow-top');wrapR.appendChild(top);var card=el('div','hv-erow-ed grid3');card.hidden=!!v.host;wrapR.appendChild(card);var h=el('span');
        var f={};f.on=row(card,'Use this provider',{type:'checkbox',value:v.on});
        f.name=row(card,'Name',{value:v.name,ph:'Newshosting'});
        f.host=row(card,'Server',{value:v.host,ph:'news.example.com'});
        f.tls=row(card,'SSL',{type:'checkbox',value:v.tls});
        f.port=row(card,'Port',{type:'number',value:v.port||(v.tls?563:119),help:'Usually 563 with SSL, 119 without.'});
        f.user=row(card,'Username',{value:v.user});
        f.pass=row(card,'Password',{type:'password',ph:v.hasPassword?'saved: leave blank to keep':''});
        f.conns=row(card,'Connections',{type:'number',value:v.connections||20,help:'Up to what your plan allows.'});
        var pw=el('div','fld');pw.appendChild(el('label','','Priority'));f.prio=el('select');prios.forEach(function(p){f.prio.appendChild(new Option(p[1],p[0]))});f.prio.value=String(v.priority||0);pw.appendChild(f.prio);
        pw.appendChild(el('div','fh','Main providers share the work. Backups are asked, in order, only for articles the ones before them do not have.'));card.appendChild(pw);
        var acts=el('div','hv-acts');var msg=el('span','msg');
        function val(){return {name:f.name.value,host:f.host.value,port:parseInt(f.port.value,10)||0,tls:f.tls.checked,user:f.user.value,password:f.pass.value,connections:parseInt(f.conns.value,10)||8,priority:parseInt(f.prio.value,10)||0,on:f.on.checked}}
        acts.appendChild(hvBtn('Test','refresh',function(b){b.disabled=true;msg.className='msg';msg.textContent='signing in…';api('POST','/usenet/test',{server:val(),index:rows.indexOf(entry)}).then(function(x){b.disabled=false;msg.className=x.ok?'msg ok':'msg err';msg.textContent=x.ok?'signed in fine':x.error}).catch(function(e){b.disabled=false;msg.className='msg err';msg.textContent=e.message})}));
        acts.appendChild(hvBtn('Remove','x',function(){if(!confirm('Remove '+(f.name.value||f.host.value||'this provider')+'?'))return;rows.splice(rows.indexOf(entry),1);wrapR.remove()}));
        acts.appendChild(msg);card.appendChild(acts);list.appendChild(wrapR);var entry={val:val};rows.push(entry);
        var live=(st.servers||[]).filter(function(x){return x.name===(v.name||v.host)})[0];
        function sum(){top.replaceChildren();var prob=live&&live.problem;top.classList.toggle('bad',!!prob);top.appendChild(el('i','hv-dot2 '+(prob?'bad':f.on.checked?'ok':'off')));
          var t=el('div','hv-erow-t');t.appendChild(el('b','',f.name.value||f.host.value||'New provider'));t.appendChild(el('span',prob?'':'hv-mono',prob?prob:[f.host.value||'no server',f.port.value,(f.conns.value||20)+' connections'].join(' · ')+(f.on.checked?'':' · off')));top.appendChild(t);
          var pr=parseInt(f.prio.value,10)||0;top.appendChild(el('span','hv-spill '+(pr?'off':'info'),pr?'Backup '+pr:'Main'));
          top.appendChild(hvBtn('Test','',function(b){b.disabled=true;msg.className='msg';msg.textContent='signing in…';card.hidden=false;sum();api('POST','/usenet/test',{server:val(),index:rows.indexOf(entry)}).then(function(x){b.disabled=false;msg.className=x.ok?'msg ok':'msg err';msg.textContent=x.ok?'signed in fine':x.error}).catch(function(e){b.disabled=false;msg.className='msg err';msg.textContent=e.message})}));
          top.appendChild(hvBtn(card.hidden?(prob?'Fix':'Edit'):'Done','',function(){card.hidden=!card.hidden;sum()},!!prob&&card.hidden))}
        [].forEach.call(card.querySelectorAll('input,select'),function(i){i.addEventListener('input',sum);i.addEventListener('change',sum)});sum()}
      (d.servers||[]).forEach(addRow);
      var add=el('div','hv-pagehead');add.appendChild(hvBtn('Add a provider','plus',function(){addRow()}));body.appendChild(add);
      saveBar(body,function(m){m.className='msg';m.textContent='saving…';
        api('PUT','/usenet',{mode:md.value,servers:rows.map(function(r){return r.val()})}).then(function(){m.className='msg ok';m.textContent='saved';setTimeout(function(){if(tab==='usenet')usenetView(body)},1200)}).catch(function(e){m.className='msg err';m.textContent=e.message})});
    }).catch(fail)}
  // VPN: the Private Internet Access tunnel inside HyveMynd, and HyveMynd's
  // own torrent client that only goes through it.
  function vpnView(body){
    api('GET','/vpn').then(function(d){body.replaceChildren();
      body.appendChild(head('VPN and built-in torrents','HyveMynd connects to your VPN (WireGuard) by itself and runs its own torrent client through that connection, downloading straight into its own folders. Nothing to set up on the server or in the arrs. Only these downloads use the VPN; the rest of HyveMynd does not. If the VPN drops, the client stops at once and carries on when it reconnects.'));
      var names={off:'Off',connecting:'Connecting…',up:'Connected',down:'Not connected'};
      var box=hvBox('Status');var l=el('div','hv-list');box.appendChild(l);body.appendChild(box);
      function drawStatus(d){var s=d.status||{};l.replaceChildren();
        function line(k,v){var r=el('div','hv-item');var t=el('div','hv-it');t.appendChild(el('b','',k));r.appendChild(t);r.appendChild(el('span','hv-note',v));l.appendChild(r)}
        var pill=hvPill(names[s.state]||s.state,s.state==='up'?'var(--ok)':s.state==='down'?'var(--bad)':'',s.state==='up');
        var top=el('div','hv-item');var tt=el('div','hv-it');tt.appendChild(el('b','','VPN'));top.appendChild(tt);top.appendChild(pill);l.appendChild(top);
        if(s.state==='up'){line('Location',(s.regionName||s.region)+(s.since&&s.since[0]!=='0'?' · connected '+ago(s.since)+' ago':''));
          line('Incoming port',s.port?String(s.port)+' (forwarded, so other people can connect: faster downloads)':'none yet');
          var c=d.client||{};line('Torrent client',c.error?'stopped: '+c.error:c.running?(c.active+' downloading · '+c.peers+' peers'):'starting…')}
        else if(s.state==='connecting')line('Location',d.region?'connecting to the chosen location':'finding the fastest port-forwarding location');
        if(s.error)l.appendChild(el('div',s.state==='up'&&s.port===0?'hv-note':'err',s.error))}
      drawStatus(d);
      timer=setInterval(function(){if(tab!=='vpn')return;api('GET','/vpn').then(drawStatus).catch(function(){})},3000);
      var PROV={pia:['Private Internet Access','Port forwarding: automatic'],proton:['Proton VPN','Port forwarding: automatic on P2P servers'],mullvad:['Mullvad','No port forwarding'],airvpn:['AirVPN','Port forwarding: enter your port'],windscribe:['Windscribe','Port forwarding: enter your port (Pro plan)'],nord:['NordVPN','No port forwarding'],surfshark:['Surfshark','No port forwarding'],ivpn:['IVPN','No port forwarding'],other:['Another provider','Any WireGuard file']};
      var STEPS={proton:'In your Proton account: Downloads › WireGuard configuration. Choose Router, turn on NAT-PMP (port forwarding), pick a P2P server, then download the file.',mullvad:'In your Mullvad account: WireGuard configuration. Generate a key, choose Linux and a location, then download the file.',airvpn:'In the AirVPN Client Area: Config Generator, choose WireGuard and a server, download the file. Add a port under Client Area › Ports and enter it below.',windscribe:'In your Windscribe account: Config Generators › WireGuard, pick a location, download the file. For port forwarding (Pro plan): My Account › Port Forwarding › request an ephemeral port with the same internal and external port, and enter it below. Ephemeral ports last 7 days, so request a new one and enter it again when it runs out.',surfshark:'In your Surfshark account: VPN › Manual setup › Router › WireGuard. Generate a key pair, pick a location, download the file.',ivpn:'In your IVPN account: WireGuard › Config generator. Add a key and download a server’s file.',other:'Download a WireGuard configuration file from your provider. Enter a forwarded port if your provider gives you one.'};
      var c=el('div','card');c.appendChild(el('h4','','VPN provider'));
      var on=row(c,'Use the VPN and the built-in torrent client',{type:'checkbox',value:d.on});
      var pw0=el('div','fld');pw0.appendChild(el('label','','Provider'));var pv=el('select');pv.hidden=true;(d.providers||Object.keys(PROV)).forEach(function(k){pv.appendChild(new Option((PROV[k]||[k])[0],k))});pv.value=d.provider||'pia';pw0.appendChild(pv);
      var grid=el('div','hv-provgrid');grid.setAttribute('role','radiogroup');grid.setAttribute('aria-label','VPN provider');pw0.appendChild(grid);c.appendChild(pw0);
      function drawGrid(){grid.replaceChildren();(d.providers||Object.keys(PROV)).forEach(function(k){var P=PROV[k]||[k,''];var b=el('button','hv-prov'+(pv.value===k?' on':''));b.type='button';b.setAttribute('role','radio');b.setAttribute('aria-checked',pv.value===k?'true':'false');
        b.appendChild(el('b','',P[0]));b.appendChild(el('span',/^Port forwarding|Any/.test(P[1])?'ok':'',P[1]));b.onclick=function(){pv.value=k;drawGrid();showProv()};grid.appendChild(b)})}
      drawGrid();
      var piaBox=el('div');c.appendChild(piaBox);
      var user=row(piaBox,'PIA username',{value:d.user,ph:'p1234567',help:'The p-number you sign in to PIA with.'});
      var pw=row(piaBox,'PIA password',{type:'password',ph:d.hasPassword?'saved: leave blank to keep':'',help:''});
      var rw=el('div','fld');rw.appendChild(el('label','','Location'));var rg=el('select');rg.appendChild(new Option('Best port-forwarding location (recommended)',''));if(d.region)rg.appendChild(new Option(d.region,d.region));rg.value=d.region||'';rw.appendChild(rg);
      rw.appendChild(el('div','fh','Locations marked “port forwarding” let other people connect to you, which makes downloads faster.'));piaBox.appendChild(rw);
      api('GET','/vpn/regions').then(function(list){var keep=rg.value;while(rg.options.length>1)rg.remove(1);list.forEach(function(g){rg.appendChild(new Option(g.name+(g.portForward?' · port forwarding':''),g.id))});rg.value=keep}).catch(function(){});
      var nordBox=el('div');c.appendChild(nordBox);
      nordBox.appendChild(el('div','fh','In your Nord account, open NordVPN › Manual setup and create an access token. HyveMynd uses it to set up WireGuard (NordLynx) for you.'));
      var tok=row(nordBox,'Access token',{type:'password',ph:d.hasToken?'saved: leave blank to keep':''});
      var ctry=row(nordBox,'Country',{value:d.country,ph:'NL, US, … (blank: Nord’s pick)'});
      var fileBox=el('div');c.appendChild(fileBox);var steps=el('div','fh');fileBox.appendChild(steps);
      var fw=el('div','fld');fw.appendChild(el('label','','WireGuard file'));var fi=el('input');fi.type='file';fi.accept='.conf,text/plain';var ta=el('textarea');ta.rows=5;ta.placeholder=d.hasWireGuard?'saved'+(d.wgServer?' (server '+d.wgServer+')':'')+': leave blank to keep, or choose or paste a new file':'choose the .conf file above, or paste its text here';
      fi.onchange=function(){var f=fi.files&&fi.files[0];if(!f)return;f.text().then(function(t){ta.value=t})};fw.appendChild(fi);fw.appendChild(ta);fileBox.appendChild(fw);
      var portBox=el('div');fileBox.appendChild(portBox);var port=row(portBox,'Your forwarded port',{value:d.port?String(d.port):'',ph:'from your account page (blank: none)'});
      function showProv(){var k=pv.value;piaBox.style.display=k==='pia'?'':'none';nordBox.style.display=k==='nord'?'':'none';fileBox.style.display=(k!=='pia'&&k!=='nord')?'':'none';steps.textContent=STEPS[k]||'';portBox.style.display=(k==='airvpn'||k==='windscribe'||k==='other')?'':'none'}
      pv.onchange=showProv;showProv();
      var tw=el('div','fld');tw.appendChild(el('label','','Where torrents go'));var tr=el('select');
      [['','TorBox first; here when TorBox is busy or not connected (recommended)'],['local','Always here (TorBox not used for torrents)'],['torbox','Always TorBox, never here']].forEach(function(o){tr.appendChild(new Option(o[1],o[0]))});tr.value=d.torrents||'';tw.appendChild(tr);
      tw.appendChild(el('div','fh',(d.torbox?'Usenet still goes to TorBox. ':'No TorBox token is set, so all torrents go here. ')+'Torrents here share at most 1 MB/s while downloading and stop sharing as soon as they finish.'));c.appendChild(tw);
      body.appendChild(c);
      saveBar(body,function(m){m.className='msg';m.textContent='saving…';
        api('PUT','/vpn',{on:on.checked,provider:pv.value,user:user.value,password:pw.value,region:rg.value,wireguard:ta.value,token:tok.value,country:ctry.value,port:parseInt(port.value,10)||0,torrents:tr.value}).then(function(){m.className='msg ok';m.textContent='saved: reconnecting (the status above updates by itself)'}).catch(function(e){m.className='msg err';m.textContent=e.message})});
    }).catch(fail)}
  // Settings › Getting started: everything HyveMynd works with, what it is
  // for and whether it is set up here.
  function startView(body){
    body.appendChild(head('Getting started','HyveMynd sits between your arrs and the places files come from. It downloads, checks, sends files through Tdarr and hands them back to the arrs to import. Here is everything it works with, and whether yours is set up.'));
    var need=el('ol','hv-start');var opt=el('div','hv-startx');body.appendChild(el('h2','hv-grph','NEEDED'));body.appendChild(need);body.appendChild(el('h2','hv-grph','OPTIONAL EXTRAS'));body.appendChild(opt);
    Promise.all([api('GET','/settings'),api('GET','/clients').catch(function(){return null}),api('GET','/vpn').catch(function(){return null}),api('GET','/system').catch(function(){return null})]).then(function(x){
      var st=x[0],cl=x[1],vp=x[2],sy=x[3];var cats=st.categories||[];
      function has(kind){return cats.some(function(c){return c.kind===kind&&c.arrUrl&&c.hasArrApiKey})}
      function pill(ok,text,tone){return el('span','hv-spill '+(ok?'ok':tone||'off'),text)}
      function step(n,name,what,how,ok,status,to,tone){var li=el('li','hv-stepc');li.appendChild(el('span','hv-stepn',String(n)));var t=el('div','hv-stept');t.appendChild(el('b','',name));t.appendChild(el('p','',what));t.appendChild(el('p','hv-note',how));li.appendChild(t);
        var r=el('div','hv-stepr');r.appendChild(pill(ok,status,tone));var a=el('button','hv-linkbtn','Open');a.type='button';a.onclick=function(){tab=to;app()};r.appendChild(a);li.appendChild(r);need.appendChild(li)}
      var dirs=!!(sy&&sy.complete&&sy.complete.totalBytes);
      step(1,'Shared folders','HyveMynd and your arrs must see downloads at the same path, so the arrs can import what HyveMynd finishes.','Give both containers the same /data mount on Unraid.',dirs,dirs?'Set up':'Check the /data mount','system');
      var arrs=has('sonarr')||has('radarr');
      step(2,'Sonarr and Radarr','They decide what to grab and send it to HyveMynd. HyveMynd reads them with their API keys and tells them when a file is ready.','In each arr: Settings › Download Clients › add HyveMynd as a SABnzbd client for usenet and a qBittorrent client for torrents (it answers as both), on port 8081 of this server.',arrs,arrs?[has('sonarr')?'Sonarr':'',has('radarr')?'Radarr':''].filter(Boolean).join(' · ')+' connected':'Not connected','arrs');
      var src=[];if(st.torbox&&st.torbox.hasToken)src.push('TorBox');if(cl&&cl.builtin&&cl.builtin.usenet)src.push((cl.usenet.servers||[]).length+' usenet providers');if(vp&&vp.on)src.push('VPN');
      step(3,'Somewhere to download from','At least one of: a TorBox account, usenet providers, or a VPN account for torrents (Private Internet Access, Proton, Mullvad, AirVPN and other major ones).','TorBox is tried first; the others take over when it is busy, down or not set up.',src.length>0,src.length?src.join(' · '):'Nothing yet','clients');
      var td=!!(st.tdarr&&st.tdarr.url);
      step(4,'Tdarr','Every media file goes through it before the arrs import it.','Give HyveMynd the Tdarr address and a library that watches its staging folder.',td,td?'Connected':'Folders only','transcode',td?'':'warn');
      function extra(name,what,how,ok,status,to,tone){var c=el('button','hv-xcard');c.type='button';var h=el('div','hv-xh');h.appendChild(el('b','',name));h.appendChild(pill(ok,status,tone));c.appendChild(h);c.appendChild(el('p','',what));c.appendChild(el('p','hv-note',how));if(to)c.onclick=function(){tab=to;app()};opt.appendChild(c)}
      extra('Prowlarr','Finds releases for your arrs. It talks to Sonarr and Radarr, not to HyveMynd.','Nothing to set here.',false,'Not needed here','', 'info');
      extra('Seerr','Powers the request box on the public page.','Address and API key.',!!(st.requests&&st.requests.enabled),st.requests&&st.requests.enabled?'Connected':'Not set up','public');
      extra('Emby or Jellyfin','Account requests, Now Playing, and telling it about new files.','Address and API key.',!!(st.playback&&st.playback.hasEmbyKey),st.playback&&st.playback.hasEmbyKey?'Connected':'Not set up','playback');
      extra('Tracearr','Watch history, used to pick what to upgrade.','Address and API key.',!!(st.playback&&st.playback.hasTracearrKey),st.playback&&st.playback.hasTracearrKey?'Connected':'Not set up','playback');
      extra('Lidarr','Music, handled the same way as TV and movies, and artist requests on the public page.','Add it like Sonarr, with its own category.',has('lidarr'),has('lidarr')?'Connected':'Not set up','arrs');
      extra('TMDB','Titles and posters for anime and requests.','A free API key from themoviedb.org.',!!(st.metadata&&st.metadata.hasTmdbKey),st.metadata&&st.metadata.hasTmdbKey?'Key saved':'Not set up','metadata');
    }).catch(fail)}
  // Upgrades: much-watched shows and movies are moved to an upgrade profile in
  // Sonarr or Radarr and searched (watch history from Tracearr).
  function upgradesView(body){
    api('GET','/attention').then(function(d){body.replaceChildren();var a=d.settings||{};
      var upDesc='When a movie gets watched by two or more people in 30 days, or someone watches three or more episodes of a show in 14 days, HyveMynd moves it to the upgrade profile below in Radarr or Sonarr and starts the search. A show is searched the season being watched and the next one first, then one more season each time Sonarr finishes, starting the next show whenever the ones in progress have fewer than 10 downloads waiting (up to 3 at once). Titles already on that profile are left alone.';
      if(!d.tracearr)body.appendChild(el('div','err','Tracearr is not set up, so there is no watch history to read. Add it under Settings › Media server.'));
      var strip=el('div','hv-upstrip');var st0=d.status||{};strip.appendChild(el('i','hv-dot2 '+(a.on?'ok':'off')));var stt=el('div','hv-erow-t');stt.appendChild(el('b','',a.on?'Upgrading what people watch':'Upgrades are off'));
      stt.appendChild(el('span','',['watch history from Tracearr',a.tvProfile?'shows → '+a.tvProfile:'',a.movieProfile?'movies → '+a.movieProfile+' (up to '+(a.moviePerDay||3)+' a day)':'',st0.lastRun&&st0.lastRun[0]!=='0'?'checked '+ago(st0.lastRun)+' ago':''].filter(Boolean).join(' · ')));strip.appendChild(stt);
      var setWrap=el('div');setWrap.hidden=!!a.on;
      strip.appendChild(hvBtn('Check now','refresh',function(b){b.disabled=true;api('POST','/attention/run').then(function(){upgradesView(body)}).catch(function(e){b.disabled=false;alertMsg(e.message)})}));
      strip.appendChild(hvBtn('Settings','',function(){setWrap.hidden=!setWrap.hidden}));body.appendChild(strip);body.appendChild(setWrap);
      var c=el('div','card');c.appendChild(el('h4','','Settings'));c.appendChild(help(upDesc+' Watch history comes from Tracearr and is checked every hour.'));
      var on=row(c,'Switch and search automatically',{type:'checkbox',value:a.on});
      function pick(label,list,val,hint){var w=el('div','fld');w.appendChild(el('label','',label));var sl=el('select');sl.appendChild(new Option('— not for '+hint+' —',''));(list||[]).forEach(function(n){sl.appendChild(new Option(n,n))});if(val&&(list||[]).indexOf(val)<0)sl.appendChild(new Option(val+' (not found)',val));sl.value=val||'';w.appendChild(sl);c.appendChild(w);return sl}
      var tv=pick('Sonarr upgrade profile',d.profiles&&d.profiles.sonarr,a.tvProfile,'shows');
      var mv=pick('Radarr upgrade profile',d.profiles&&d.profiles.radarr,a.movieProfile,'movies');
      c.appendChild(help('Shows go one at a time: the next show starts once every season of the one before has been searched.'));
      var md=row(c,'Most movies a day (Radarr)',{type:'number',value:a.moviePerDay||3,help:'Anything over the limit waits its turn; movies you add yourself do not count.'});
      setWrap.appendChild(c);
      saveBar(setWrap,function(m){m.className='msg';m.textContent='saving…';
        api('PUT','/attention',{on:on.checked,tvProfile:tv.value,movieProfile:mv.value,perDay:a.perDay||3,moviePerDay:parseInt(md.value,10)||3}).then(function(){m.className='msg ok';m.textContent='saved';setTimeout(function(){upgradesView(body)},800)}).catch(function(e){m.className='msg err';m.textContent=e.message})});
      split(setWrap);
      var st=d.status||{actions:[]},box=hvBox('Being upgraded');

      if(st.lastError)box.appendChild(el('div','err',st.lastError));
      var l=el('div','hv-list');
      if(!(st.actions||[]).length)l.appendChild(el('div','hv-empty','Nothing moved yet.'));
      (st.actions||[]).forEach(function(x){var r=el('div','hv-item');r.appendChild(hvPill(x.kind==='movie'?'Movie':'Show',x.kind==='movie'?'var(--movie)':'var(--tv)',true));
        var t=el('div','hv-it');t.appendChild(el('b','',x.title));
        var sub=x.reason+(x.added?' · added to Radarr ('+x.added+') on ':' · moved to ')+x.profile;if(x.seasons&&x.seasons.length)sub+=' · searched season'+(x.seasons.length>1?'s ':' ')+x.seasons.join(', ');if(x.handoff)sub+=' · every season searched';else if(x.kind==='show'&&x.seasons&&x.seasons.length)sub+=x.stalled?' · stalled: nothing upgraded for 12 hours; it carries on when Sonarr gets through its queue':' · next season when Sonarr finishes these';
        t.appendChild(el('span','',sub));if(x.progress&&!x.reverted)t.appendChild(upBar(x));if(x.reverted)t.appendChild(el('span','hv-warn-t','Moved back: '+(x.note||'undone')));if(x.error)t.appendChild(el('span','hv-bad',x.error));r.appendChild(t);r.appendChild(el('span','hv-note',ago(x.at)+' ago'));
        if(!x.reverted&&x.previous&&!x.error){r.appendChild(hvBtn('Undo','back',function(b){if(!confirm('Move '+x.title+' back to its old profile and never move it again? Downloads already started keep going.'))return;b.disabled=true;api('POST','/attention/undo',{kind:x.kind,category:x.category,arrId:x.arrId}).then(function(){upgradesView(body)}).catch(function(e){b.disabled=false;alertMsg(e.message)})}))}
        l.appendChild(r)});
      box.appendChild(l);
      var dd=st.decide||[];if(dd.length){var db=hvBox('Needs your decision');db.appendChild(el('p','hv-note','Watched a lot, but not in Radarr, and HyveMynd was not sure enough to add it. Add puts it on the upgrade profile, tied to its folder if Radarr has one, and searches.'));var dl=el('div','hv-list');
        dd.forEach(function(x){var r=el('div','hv-item');r.appendChild(hvPill('Movie','var(--movie)',true));var t=el('div','hv-it');t.appendChild(el('b','',x.title+(x.year?' ('+x.year+')':'')));t.appendChild(el('span','',x.reason));t.appendChild(el('span','hv-warn-t',x.why));r.appendChild(t);
          var bs=el('div','hv-acts');[['Add to Radarr',true],['Dismiss',false]].forEach(function(o){bs.appendChild(hvBtn(o[0],o[1]?'plus':'x',function(b){b.disabled=true;api('POST','/attention/decide',{tmdb:x.tmdb,add:o[1]}).then(function(){upgradesView(body)}).catch(function(e){b.disabled=false;alertMsg(e.message)})},o[1]))});r.appendChild(bs);dl.appendChild(r)});
        db.appendChild(dl);body.appendChild(db)}
      body.appendChild(box);
      var q=st.queue||[];if(q.length){var qb=hvBox('Waiting for their turn');qb.appendChild(el('p','hv-note','Shows start one at a time: the next starts when the shows in progress have fewer than 10 downloads waiting in Sonarr between them (up to 3 shows at once), or when the show in progress is done or has upgraded nothing for 12 hours; movies over the daily limit go on the next days. Oldest first, even after the watching is older than the window that counted it.'));var ql=el('div','hv-list');
        q.forEach(function(x,i){var r=el('div','hv-item slim');r.appendChild(hvPill(String(i+1),'var(--dim)'));var t=el('div','hv-it');t.appendChild(el('b','',x.title));t.appendChild(el('span','',(x.kind==='movie'?'Movie':'Show')+' · '+x.reason));r.appendChild(t);r.appendChild(el('span','hv-note','waiting '+ago(x.since)));ql.appendChild(r)});
        qb.appendChild(ql);body.appendChild(qb)}
    }).catch(fail)}
  // Upgrade progress: one segment per season, filled to the share of its
  // episodes already at the upgrade quality (Sonarr's own cutoff check).
  function upBar(x){var p=x.progress,w=el('div','up-w');
    if(!p.total){w.appendChild(el('span','hv-note',x.kind==='movie'?'no file yet':'no episode files yet'));return w}
    var pct=Math.floor(100*p.done/p.total);
    if(x.kind==='show'&&(p.seasons||[]).length){var b=el('div','up-ss');b.setAttribute('role','img');b.setAttribute('aria-label',pct+'% upgraded');
      p.seasons.forEach(function(sd){var g=el('span','up-seg'+(sd.done===sd.total?' full':''));g.style.flexGrow=String(Math.max(1,Math.sqrt(sd.total)));var f=el('i');f.style.width=(100*sd.done/sd.total).toFixed(1)+'%';g.appendChild(f);g.appendChild(el('b','','S'+sd.season));g.title='Season '+sd.season+': '+sd.done+' of '+sd.total+' upgraded';b.appendChild(g)});w.appendChild(b)}
    w.appendChild(el('span','up-pct'+(pct===100?' full':''),x.kind==='movie'?(p.done?'upgraded':'waiting for a better copy'):pct+'% upgraded · '+p.done+' of '+p.total+' episodes'));return w}
  function alertMsg(t){var d=el('div','err',t);root.appendChild(d);setTimeout(function(){d.remove()},6000)}
  // Overview (pinned v2 admin): health pills, Needs you, Pipeline now, Just finished, Who runs what.
  var STAGE6=[['found','Found','var(--st-found)'],['download','Download','var(--st-down)'],['transfer','Transfer','var(--st-transfer)'],['convert','Convert','var(--st-convert)'],['ready','Ready','var(--st-ready)']];
  function hvHealth(label,value,tone){var p=el('div','hv-health '+(tone||'ok'));p.appendChild(el('i'));p.appendChild(el('span','',label));if(value)p.appendChild(el('b','',value));return p}
  // Home (pinned v4): health tiles, what needs you, what is moving, what just finished.
  function overviewView(body){
    var hello=el('div','hv-hello');body.appendChild(hello);
    var tiles=el('section','hv-tiles6');tiles.setAttribute('aria-label','Health');body.appendChild(tiles);
    var need=el('section','hv-need');body.appendChild(need);
    var g=el('div','hv-two');var mv=hvBox('Moving now'),fin=hvBox('Just finished');g.appendChild(mv);g.appendChild(fin);body.appendChild(g);
    var sysInfo=null,cl=null,vp=null,reqList=[],music=[];
    function tile(label,line,tone,to){var b=el('button','hv-tile6 '+(tone||'ok'));b.type='button';var h=el('span','hv-th');h.appendChild(el('i'));h.appendChild(document.createTextNode(label));b.appendChild(h);b.appendChild(el('span','hv-tline',line));b.onclick=function(){tab=to;app()};tiles.appendChild(b)}
    function drawTiles(){tiles.replaceChildren();var s=sysInfo;if(!s)return;
      if(s.torbox&&s.torbox.configured)tile('TorBox',s.torbox.backoffUntil?'New adds paused by TorBox':'Working · '+((s.torbox.used||0)+(s.torbox.torrentUsed||0))+' adds this hour',s.torbox.backoffUntil?'warn':'ok','torbox');else tile('TorBox','Not set up','dim','clients');
      if(cl){if(cl.builtin.usenet){var sv=cl.usenet.servers||[],bad=sv.filter(function(x){return x.problem});tile('Usenet',bad.length?bad.length+' of '+sv.length+' providers failing: '+bad[0].name:sv.length+' provider'+(sv.length===1?'':'s')+', no problems',bad.length&&bad.length===sv.length?'bad':bad.length?'warn':'ok','usenet')}else tile('Usenet','No providers','dim','usenet')}
      if(vp){var st=vp.status||{};tile('VPN',st.state==='up'?(st.regionName||'Connected')+(st.port?' · port '+st.port+' open':' · no forwarded port'):st.state==='off'?'Off':st.state==='connecting'?'Connecting…':'Down: '+(st.error||''),st.state==='up'?(st.port?'ok':'warn'):st.state==='off'?'dim':st.state==='connecting'?'warn':'bad','vpn')}
      tile('Tdarr',s.tdarr.api?(s.tdarr.inFlight+' of '+s.tdarr.max+' busy'):'Folders only',s.tdarr.api?'ok':'dim','transcode');
      var fr=s.complete&&s.complete.totalBytes?s.complete.freeBytes/s.complete.totalBytes:null;if(fr!=null)tile('Storage',Math.round(fr*100)+'% free in the complete folder',fr<.1?'bad':fr<.2?'warn':'ok','system');
      tile('Intake',s.paused?'Paused: new grabs wait':'Taking grabs from the arrs',s.paused?'warn':'ok','system')}
    function needRow(kind,tone,title,why,btns){var r=el('div','hv-needrow '+tone);r.appendChild(el('span','hv-needk',kind));var t=el('div','hv-needt');t.appendChild(el('b','',title));t.appendChild(el('span','',why));r.appendChild(t);var a=el('div','hv-acts');btns.forEach(function(b){a.appendChild(b)});r.appendChild(a);return r}
    function drawNeed(){need.replaceChildren();var jobs=lastRows.filter(function(r){return r.attention&&r.state!=='cleaned'});var nreq=reqList.length+music.length;
      var h=el('h2','','Needs you');if(jobs.length+(nreq?1:0))h.appendChild(el('span','hv-n bad',String(jobs.length+(nreq?1:0))));need.appendChild(h);
      if(!jobs.length&&!nreq){need.appendChild(el('div','hv-empty','Nothing needs you right now.'));return}
      jobs.forEach(function(j){var failed=j.state==='failed';var why=((j.flag?j.flag+' ':'')+(j.reason||'')).trim()||j.state;var btns=[];
        if(failed){btns.push(hvBtn('Retry','',function(b){b.disabled=true;doAction(j.id,'retry')},true));btns.push(hvBtn('Clean up','',function(b){b.disabled=true;doAction(j.id,'cleanup')}))}
        else btns.push(hvBtn('Retry now','',function(b){b.disabled=true;doAction(j.id,'retry')},true));
        btns.push(hvBtn('Details','',function(){tab='jobs';detailId=j.id;app()}));
        need.appendChild(needRow(failed?'FAILED':'STUCK',failed?'bad':'warn',j.name,why,btns))});
      if(nreq){var parts=[];if(reqList.length)parts.push(reqList.length+' media');if(music.length)parts.push(music.length+' music');
        need.appendChild(needRow('REQUESTS','req',nreq+' waiting for approval',parts.join(' and ')+' request'+(nreq===1?'':'s'),[hvBtn('Review','',function(){tab='requests';app()},true)]))}}
    var STEPS=[['found','Grabbed'],['download','Downloading'],['transfer','Transferring'],['convert','Tdarr'],['ready','Importing']];
    function drawMoving(){var b=mv.querySelector('.hv-steps6')||mv.appendChild(el('div','hv-steps6'));b.replaceChildren();
      STEPS.forEach(function(st){var n=lastRows.filter(function(r){var m=jobStage(r);return m[0]===st[0]&&m[1]!=='done'&&m[1]!=='failed'}).length;var c=el('div','hv-step6 k-'+st[0]);c.appendChild(el('b','',String(n)));c.appendChild(el('span','',st[1]));b.appendChild(c)});
      var today=lastRows.filter(function(r){return (r.state==='imported'||r.state==='cleaned')&&new Date(r.changed)>new Date(Date.now()-864e5)}).length;var c=el('div','hv-step6 k-done');c.appendChild(el('b','',String(today)));c.appendChild(el('span','','Done today'));b.appendChild(c);
      var l=mv.querySelector('.hv-list')||mv.appendChild(el('div','hv-list'));l.replaceChildren();
      var moving=lastRows.filter(function(r){return WORKING[r.group]}).sort(function(a,b){return (b.progress||0)-(a.progress||0)}).slice(0,4);
      if(!moving.length){l.appendChild(el('div','hv-empty','Nothing is moving right now.'));return}
      moving.forEach(function(j){var m=jobStage(j);var r=el('button','hv-mrow');r.type='button';r.onclick=function(){tab='jobs';detailId=j.id;app()};var t=el('div','hv-mt');t.appendChild(el('b','',j.name));t.appendChild(el('span','',jobStageText(j)));r.appendChild(t);var bar=el('div','hv-bar');var f=el('span','k-'+m[0]);f.style.width=Math.round((j.progress||0)*100)+'%';bar.appendChild(f);r.appendChild(bar);l.appendChild(r)})}
    function drawFin(){var list=fin.querySelector('.hv-list')||fin.appendChild(el('div','hv-list'));list.replaceChildren();
      var done=lastRows.filter(function(r){return r.state==='imported'||r.state==='cleaned'}).sort(function(a,b){return new Date(b.changed)-new Date(a.changed)}).slice(0,6);
      if(!done.length){list.appendChild(el('div','hv-empty','Nothing finished lately.'));return}
      done.forEach(function(j){var k=KIND[j.source]||[j.source||'',''];var row=el('div','hv-frow');var kk=el('span','hv-fk',k[0]);kk.style.color=k[1];row.appendChild(kk);row.appendChild(el('span','hv-ft',j.name));row.appendChild(el('span','hv-age',ago(j.changed)+' ago'));list.appendChild(row)})}
    function drawHello(){hello.replaceChildren();var h=new Date().getHours();hello.appendChild(el('h2','',h<12?'Good morning':h<18?'Good afternoon':'Good evening'));
      var bad=lastRows.filter(function(r){return r.attention&&r.state!=='cleaned'}).length+(reqList.length+music.length?1:0),moving=lastRows.filter(function(r){return WORKING[r.group]}).length;
      hello.appendChild(el('p','',[bad?bad+(bad===1?' thing needs':' things need')+' you':'nothing needs you',moving?moving+' download'+(moving===1?'':'s')+' moving':'nothing moving'].join(' · ')))}
    function draw(){drawHello();drawNeed();drawMoving();drawFin();railCounts(lastRows)}
    function refresh(){loadReqs().then(function(l){reqList=l||[];music=reqMusic||[];setReqBadge(reqList.length+music.length);draw()})}
    function poll(){api('GET','/jobs').then(function(d){lastRows=Array.isArray(d)?d:(d.jobs||[]);draw()}).catch(fail)}
    function pollSys(){Promise.all([api('GET','/system').catch(function(){return null}),api('GET','/clients').catch(function(){return null}),api('GET','/vpn').catch(function(){return null})]).then(function(x){sysInfo=x[0];cl=x[1];vp=x[2];drawTiles()})}
    poll();pollSys();refresh();timer=setInterval(function(){if(tab!=='overview')return;poll();pollSys();refresh()},5000);
  }
  function doAction(id,a){api('POST','/jobs/'+encodeURIComponent(id)+'/'+a).then(function(){app()}).catch(function(e){if(e.status===401)fail(e);else{alert(e.message);app()}})}
  function detail(body,id){
    stopLive();body.replaceChildren();
    var back=el('button','link','← all jobs');back.onclick=function(){detailId=null;app()};body.appendChild(back);
    api('GET','/jobs/'+encodeURIComponent(id)).then(function(j){
      body.appendChild(el('h3','',j.name));
      var c=el('div','card');[['state',j.state],['reason',j.reason||'-'],['id (download id)',j.id],['category',j.category],['source',j.source],['backend',j.backend||'-'],['complete folder',j.completeDir||'-'],['files',(j.files||[]).join(', ')||'-']].forEach(function(p){var d=el('div');d.appendChild(el('span','prompt',p[0]+': '));d.appendChild(document.createTextNode(p[1]));c.appendChild(d)});
      body.appendChild(c);body.appendChild(el('div','sec','── history '));
      var t=el('table');(j.history||[]).slice().reverse().forEach(function(h){var r=el('tr');r.appendChild(el('td','mono',new Date(h.at).toLocaleString()));r.appendChild(el('td','',(h.from||'·')+' → '+h.to));r.appendChild(el('td','reason',h.reason||''));t.appendChild(r)});body.appendChild(t);
    }).catch(fail);
  }
  // ------------------------------------------------------------ settings
  var S=null;
  function clone(o){return JSON.parse(JSON.stringify(o))}
  // save sends the whole settings object every time, so no tab can wipe another tab's data.
  // Secrets are never sent back unless typed: a blank secret means "unchanged".
  function save(mut,msg){
    var b=clone(S);mut(b);msg.className='msg';msg.textContent='saving…';
    return api('PUT','/settings',b).then(function(r){S=r;msg.className='msg ok';msg.textContent='saved';setTimeout(function(){if(msg.textContent==='saved')msg.textContent=''},3000)}).catch(function(e){msg.className='msg err';msg.textContent=e.message})
  }
  function help(t){return el('p','hint',t)}
  function head(title,sub){var d=el('div','pane-head');d.appendChild(el('h3','',title));if(sub)d.appendChild(el('p','hint',sub));return d}
  // one labelled input with help text; returns the input
  function row(box,label,opts){
    opts=opts||{};var w=el('div','fld');w.appendChild(el('label','',label));
    var i=el('input');i.type=opts.type||'text';i.value=opts.value==null?'':opts.value;i.autocomplete='off';if(opts.ph)i.placeholder=opts.ph;if(opts.type==='checkbox'){i.checked=!!opts.value;i.style.width='auto'}
    w.appendChild(i);if(opts.help)w.appendChild(el('div','fh',opts.help));box.appendChild(w);return i
  }
  function chip(on,text){return el('span','chip2 '+(on?'ok':'dim'),text)}
  function saveBar(body,fn){var bar=el('div','savebar');var b=el('button','go','save');var m=el('span','msg');b.onclick=function(){fn(m)};bar.appendChild(b);bar.appendChild(m);body.appendChild(bar)}

  function settingsView(body){
    api('GET','/settings').then(function(s0){S=s0;pane(body)}).catch(fail)
  }
  function pane(body){
    body.replaceChildren();
    ({arrs:arrsPane,clients:clientsPane,transcode:transcodePane,metadata:metadataPane,playback:playbackPane,public:publicPane,rss:rssPane,system:systemPane})[tab](body);
    if(tab!=='system'&&tab!=='rss')split(body);
  }
  // A settings card becomes a row: its title and explanation on the left, the fields on the right.
  function split(body){
    [].filter.call(body.children,function(c){return c.classList.contains('card')&&c.firstChild&&c.firstChild.tagName==='H4'}).forEach(function(c){
      var l=el('div','sl'),r=el('div','sr');l.appendChild(c.firstChild);
      if(c.firstChild&&c.firstChild.classList&&c.firstChild.classList.contains('hint'))l.appendChild(c.firstChild);
      while(c.firstChild)r.appendChild(c.firstChild);c.classList.add('setting');c.appendChild(l);c.appendChild(r)})
  }

  function arrsPane(body){
    body.appendChild(head('Arrs & categories','Each category is a label an arr sends when it grabs something. It decides which arr confirms the import, and which folder the finished download lands in.'));
    var conn=el('div','card');conn.appendChild(el('h4','','Connect an arr to HyveMynd'));
    var host=location.hostname;
    conn.appendChild(help('In Sonarr, Radarr or Lidarr: Settings → Download Clients → add. Use SABnzbd for usenet and qBittorrent for torrents.'));
    [['host',host],['port','8081 (the LAN-only machine port)'],['SABnzbd API key','the client password (see Settings › Server)'],['qBittorrent login','username '+S.clientUser+' and the client password'],['category','one of the names below, exactly']].forEach(function(p){var d=el('div','kv');d.appendChild(el('span','k',p[0]));d.appendChild(el('span','v',p[1]));conn.appendChild(d)});
    body.appendChild(conn);
    body.appendChild(el('h2','hv-grph','CATEGORIES'));var cats=el('div','hv-erows');body.appendChild(cats);
    function add(c){
      var cw=el('div','hv-erow');var top=el('div','hv-erow-top');cw.appendChild(top);var card=el('div','hv-erow-ed');card.hidden=!!c.name;cw.appendChild(card);var f={};var g=el('div','grid3');f.orig=c.name||'';
      f.name=row(g,'category name',{value:c.name,help:'what the arr sends, e.g. tv'});
      var w=el('div','fld');w.appendChild(el('label','','arr type'));var k=el('select');[['sonarr','sonarr'],['radarr','radarr'],['lidarr','lidarr'],['rss','no arr (RSS / watch folder)'],['files','plain files (software and anything else, kept as it is)']].forEach(function(p){var x=p[0];var o=el('option','',p[1]);o.value=x;if(c.kind===x)o.selected=true;k.appendChild(o)});w.appendChild(k);g.appendChild(w);f.kind=k;
      f.arrUrl=row(g,'arr address',{value:c.arrUrl,ph:'http://10.0.0.5:8989',help:'so HyveMynd can confirm the import'});
      f.arrApiKey=row(g,'arr API key',{type:'password',ph:c.hasArrApiKey?'set — leave blank to keep':'',help:'in the arr: Settings → General'});
      f.transcode=row(g,'transcode with Tdarr',{type:'checkbox',value:c.transcode,help:'off for music'});
      f.libraryDir=row(g,'library folder',{value:c.libraryDir,ph:'/media/Anime',help:'“no arr” categories: finished episodes are matched to a show folder here. Plain files: each download is moved here as its own folder, every file kept, with no malicious-file check and no Tdarr.'});
      f.keepNames=row(g,'keep release names',{type:'checkbox',value:c.keepNames,help:'off (the default): a placed episode is named after its show folder, the way that folder names its files'});
      var pw=el('div','fld');pw.appendChild(el('label','','if the transcode fails'));var pf=el('select');[['fail','fail the grab (the arr blocklists and searches again)'],['passthrough','keep the original file']].forEach(function(x){var o=el('option','',x[1]);o.value=x[0];if((c.onTranscodeFail||'fail')===x[0])o.selected=true;pf.appendChild(o)});pw.appendChild(pf);g.appendChild(pw);f.onFail=pf;
      card.appendChild(g);var tt=el('button','','Test arr connection');var tmsg=el('span','msg');tmsg.style.marginLeft='12px';
      tt.onclick=function(){tmsg.className='msg';tmsg.textContent='testing…';api('POST','/arrs/test',{name:f.name.value,origName:f.orig,kind:f.kind.value,url:f.arrUrl.value,apiKey:f.arrApiKey.value}).then(function(r){tmsg.className='msg ok';tmsg.textContent='connected: '+r.app+' '+r.version}).catch(function(e){tmsg.className='msg err';tmsg.textContent=e.message})};
      card.appendChild(tt);card.appendChild(tmsg);
      var sw=el('button','','Place files already in this category\u2019s folder');sw.style.marginLeft='12px';sw.title='uses the saved library folder; save first if you just changed it';var smsg=el('span','msg');smsg.style.marginLeft='12px';
      sw.onclick=function(){smsg.className='msg';smsg.textContent='placing…';api('POST','/library/sweep',{name:f.orig||f.name.value}).then(function(r){smsg.className='msg ok';smsg.textContent=r.result}).catch(function(e){smsg.className='msg err';smsg.textContent=e.message})};
      if(c.libraryDir&&c.kind!=='files'){card.appendChild(sw);card.appendChild(smsg)}card.appendChild(document.createElement('br'));
      var rm=el('button','bad','Remove category');rm.onclick=function(){cw.remove()};card.appendChild(rm);cw._f=f;cats.appendChild(cw);
      var KN={sonarr:['Sonarr','var(--tv)'],radarr:['Radarr','var(--movie)'],lidarr:['Lidarr','var(--music)'],rss:['No arr: feeds and folders','var(--anime)'],files:['Plain files','var(--dim)']};
      function sum(){top.replaceChildren();var kn=KN[f.kind.value]||[f.kind.value,'var(--dim)'];var arr=/sonarr|radarr|lidarr/.test(f.kind.value);
        top.appendChild(el('i','hv-dot2 '+(arr&&!f.arrUrl.value?'bad':'ok')));var t=el('div','hv-erow-t');var nb=el('b','',f.name.value||'New category');t.appendChild(nb);
        t.appendChild(el('span','',[arr?(f.arrUrl.value||'no address'):(f.libraryDir.value||'complete folder'),f.kind.value==='files'?'kept as it is':f.transcode.checked?'through Tdarr':'no Tdarr'].join(' · ')));top.appendChild(t);
        top.appendChild(hvPill(kn[0],kn[1]));top.appendChild(hvBtn(card.hidden?'Edit':'Done','',function(){card.hidden=!card.hidden;sum()}))}
      [].forEach.call(card.querySelectorAll('input,select'),function(i){i.addEventListener('input',sum);i.addEventListener('change',sum)});sum()
    }
    S.categories.forEach(add);
    var a=hvBtn('Add a category','plus',function(){add({kind:'radarr',transcode:true})});a.style.marginTop='10px';body.appendChild(a);
    saveBar(body,function(m){save(function(b){b.categories=[].map.call(cats.children,function(c){var f=c._f;return {name:f.name.value,origName:f.orig,kind:f.kind.value,libraryDir:f.libraryDir.value,arrUrl:f.arrUrl.value,arrApiKey:f.arrApiKey.value,transcode:f.transcode.checked,onTranscodeFail:f.onFail.value,keepNames:f.keepNames.checked}})},m)})
  }

  function clientsPane(body){
    body.appendChild(head('TorBox','HyveMynd sends downloads to TorBox first when a token is set. When TorBox is busy, down or not set up, its own usenet downloader (Downloads › Usenet) and torrent client (Downloads › VPN) take over.'));
    var tb=el('div','card');var h=el('h4','','TorBox ');h.appendChild(chip(S.torbox.hasToken,S.torbox.hasToken?'token set':'no token'));tb.appendChild(h);
    var g=el('div','grid3');
    var tok=row(g,'API token',{type:'password',ph:S.torbox.hasToken?'set — leave blank to keep':'paste your token',help:'TorBox → Settings → API'});
    var cph=row(g,'new downloads per hour',{type:'number',value:S.torbox.createPerHour,help:'TorBox’s limit on creating downloads'});
    var pa=row(g,'files copied at once',{type:'number',value:S.torbox.pullersAtOnce,help:'how many finished files are pulled down in parallel'});
    var lg=row(g,'seconds between link requests',{type:'number',value:S.torbox.linkGapSeconds,help:'spaces out requests so TorBox does not throttle'});
    tb.appendChild(g);body.appendChild(tb);
    saveBar(body,function(m){save(function(b){
      b.torbox={token:tok.value,createPerHour:+cph.value,pullersAtOnce:+pa.value,linkGapSeconds:+lg.value}},m).then(function(){pane(body)})})
  }

  function transcodePane(body){
    body.appendChild(head('Tdarr','Videos for categories with transcode on are handed to a dedicated Tdarr library. HyveMynd watches Tdarr over its API, so it can show progress and notice failures.'));
    var c0=el('div','card');var h0=el('h4','','Connection ');h0.appendChild(chip(!!S.tdarr.url,S.tdarr.url?'configured':'not set — folder hand-off only'));c0.appendChild(h0);
    var g0=el('div','grid3');
    var url=row(g0,'Tdarr address',{value:S.tdarr.url,ph:'http://10.0.0.8:8265',help:'the Tdarr server, port 8265 by default'});
    var key=row(g0,'API key',{type:'password',ph:S.tdarr.hasApiKey?'set — leave blank to keep':'only if Tdarr has authentication on',help:'Tdarr → Tools → API Keys'});
    var lib=row(g0,'library ID',{value:S.tdarr.libraryId,help:'the ID of the dedicated HyveMynd library (shown in the library’s settings)'});
    c0.appendChild(g0);var tb=el('button','','test connection');var tm=el('span','msg');tm.style.marginLeft='12px';
    tb.onclick=function(){tm.className='msg';tm.textContent='testing…';api('POST','/tdarr/test',{url:url.value,apiKey:key.value}).then(function(r){tm.className='msg ok';tm.textContent='connected: '+r.nodes+' node(s), '+r.busyWorkers+' busy worker(s)'}).catch(function(e){tm.className='msg err';tm.textContent=e.message})};
    c0.appendChild(tb);c0.appendChild(tm);body.appendChild(c0);
    var c=el('div','card');c.appendChild(el('h4','','Folders and limits'));var g=el('div','grid3');
    var inn=row(g,'input folder (library source)',{value:S.tdarr.inDir,help:'the same path inside HyveMynd and Tdarr'});
    var out=row(g,'output folder (library output)',{value:S.tdarr.outDir});
    var mx=row(g,'jobs with Tdarr at once',{type:'number',value:S.tdarr.maxInFlight,help:'others wait their turn; waiting is not a failure'});
    var st=row(g,'stall timeout (minutes)',{type:'number',value:S.tdarr.stallMinutes,help:'a worker holding a file with no progress this long counts as failed'});
    var to=row(g,'give up waiting after (minutes)',{type:'number',value:S.tdarr.timeoutMinutes,help:'when Tdarr produces nothing at all'});
    c.appendChild(g);body.appendChild(c);
    var cc=el('div','card');cc.appendChild(el('h4','','Per category'));
    S.categories.forEach(function(k){var d=el('div','kv');d.appendChild(el('span','k',k.name));d.appendChild(chip(k.transcode,k.transcode?'transcode on':'skips Tdarr'));if(k.transcode)d.appendChild(el('span','v','on failure: '+(k.onTranscodeFail==='passthrough'?'keep the original':'fail the grab')));cc.appendChild(d)});
    cc.appendChild(help('Change transcode and the failure policy on the Arrs & categories tab.'));body.appendChild(cc);
    saveBar(body,function(m){save(function(b){b.tdarr={url:url.value,apiKey:key.value,libraryId:lib.value,maxInFlight:+mx.value,stallMinutes:+st.value,inDir:inn.value,outDir:out.value,timeoutMinutes:+to.value}},m)})
  }

  function metadataPane(body){
    var M=S.metadata||{};
    body.appendChild(head('Metadata','Show information (seasons, episodes, other titles) comes from TMDB with your own free API key. Two anime lists add more titles and IDs; they are optional and refresh weekly.'));
    var c=el('div','card');var h=el('h4','','TMDB ');h.appendChild(chip(M.hasTmdbKey,M.hasTmdbKey?'key set':'no key'));c.appendChild(h);
    var g=el('div','grid3');
    var k=row(g,'API key',{type:'password',ph:M.hasTmdbKey?'set — leave blank to keep':'TMDB → Settings → API',help:'the short API key or the long read access token both work'});
    c.appendChild(g);
    var tb=el('button','','test key');var tm=el('span','msg');tm.style.marginLeft='12px';
    tb.onclick=function(){tm.className='msg';tm.textContent='testing…';api('POST','/metadata/test',{key:k.value}).then(function(){tm.className='msg ok';tm.textContent='TMDB accepted the key'}).catch(function(e){tm.className='msg err';tm.textContent=e.message})};
    c.appendChild(tb);c.appendChild(tm);body.appendChild(c);
    var c2=el('div','card');c2.appendChild(el('h4','','Anime lists'));
    var g2=el('div','grid3');
    var tu=row(g2,'titles list address',{value:M.animeTitlesUrl,ph:'default: anime-offline-database',help:'empty for the default, off to turn it off'});
    var iu=row(g2,'ID mapping address',{value:M.animeIdsUrl,ph:'default: Kometa Anime-IDs',help:'empty for the default, off to turn it off'});
    c2.appendChild(g2);
    var ls=el('p','hint','status: checking…');c2.appendChild(ls);
    function drawStatus(){api('GET','/metadata/status').then(function(r){
      if(!r.enabled){ls.textContent='metadata needs the database (UNDERMYND_STORE is set to json)';return}
      var L=r.lists||{};if(!L.checked||L.checked.indexOf('0001')===0){ls.textContent='not downloaded yet';return}
      ls.textContent='last checked '+new Date(L.checked).toLocaleString()+' · '+(L.titles||0)+' anime with titles'+(L.titlesDate?' (list dated '+L.titlesDate+')':'')+' · '+(L.ids||0)+' ID mappings'+(L.titlesError?' · titles: '+L.titlesError:'')+(L.idsError?' · IDs: '+L.idsError:'')
    }).catch(function(){ls.textContent='status unknown'})}
    drawStatus();
    var rb=el('button','','download now');var rm=el('span','msg');rm.style.marginLeft='12px';
    rb.onclick=function(){rm.className='msg';rm.textContent='starting…';api('POST','/metadata/lists/refresh',{}).then(function(){rm.className='msg ok';rm.textContent='downloading in the background; check back in a minute';setTimeout(drawStatus,60000)}).catch(function(e){rm.className='msg err';rm.textContent=e.message})};
    c2.appendChild(rb);c2.appendChild(rm);body.appendChild(c2);
    var cr=el('p','hint');cr.appendChild(document.createTextNode('Attribution for these sources is on the '));var a=el('a','','credits page');a.href='/credits';a.target='_blank';cr.appendChild(a);cr.appendChild(document.createTextNode('.'));body.appendChild(cr);
    saveBar(body,function(m){save(function(b){b.metadata={tmdbKey:k.value,animeTitlesUrl:tu.value,animeIdsUrl:iu.value}},m).then(function(){pane(body)})})
  }

  function playbackPane(body){
    var P=S.playback||{};
    body.appendChild(head('Media server','Feeds the public page: Now Playing and Recently Added come from Tracearr (music is added from Lidarr). Emby is only used to hide whole libraries from Now Playing.'));
    var c=el('div','card');var h=el('h4','','Tracearr ');h.appendChild(chip(!!P.tracearrUrl&&P.hasTracearrKey,P.tracearrUrl&&P.hasTracearrKey?'configured':'not set'));c.appendChild(h);
    var g=el('div','grid3');
    var tu=row(g,'Tracearr address',{value:P.tracearrUrl,ph:'http://10.0.0.8:3000'});
    var tk=row(g,'Public API key',{type:'password',ph:P.hasTracearrKey?'set — leave blank to keep':'Tracearr → Settings → Public API',help:'read-only access to streams and recently added'});
    c.appendChild(g);body.appendChild(c);
    var c2=el('div','card');var h2=el('h4','','Hidden libraries ');h2.appendChild(chip(!!P.hiddenLibraries,P.hiddenLibraries?'hiding '+P.hiddenLibraries:'none hidden'));c2.appendChild(h2);
    var g2=el('div','grid3');
    var eu=row(g2,'Emby address',{value:P.embyUrl,ph:'http://10.0.0.8:8096'});
    var ek=row(g2,'Emby API key',{type:'password',ph:P.hasEmbyKey?'set — leave blank to keep':'Emby → Dashboard → API Keys'});
    var hl=row(g2,'hidden library IDs',{value:P.hiddenLibraries,ph:'9',help:'comma-separated Emby library IDs; streams from these never appear on the public page. A stream that cannot be placed in a library is withheld.'});
    c2.appendChild(g2);
    var tb=el('button','','test connections');var tm=el('span','msg');tm.style.marginLeft='12px';
    tb.onclick=function(){tm.className='msg';tm.textContent='testing…';api('POST','/playback/test',{tracearrUrl:tu.value,tracearrKey:tk.value,embyUrl:eu.value,embyKey:ek.value}).then(function(r){tm.className='msg ok';tm.textContent=[r.tracearr?'Tracearr ok':'',r.emby?'Emby ok: '+r.emby:''].filter(Boolean).join(' · ')||'nothing to test'}).catch(function(e){tm.className='msg err';tm.textContent=e.message})};
    c2.appendChild(tb);c2.appendChild(tm);body.appendChild(c2);
    var ps=el('p','hint','last poll: checking…');body.appendChild(ps);api('GET','/playback/status').then(function(r){ps.textContent='last poll: '+r.status}).catch(function(){ps.textContent='last poll: unknown'});
    var rf=row(g2,'tell the media server about new files',{type:'checkbox',value:!!P.refreshAfterPlace,help:'Off by default: Emby and Jellyfin find new files on their own. On: each file the simple importer places is announced so it shows up at once.'});
    var pf=row(g2,'path here',{value:P.pathFrom||'',ph:'/media',help:'Only when the media server sees the library under another path.'});var pt=row(g2,'path on the media server',{value:P.pathTo||'',ph:'/mnt/user/media'});
    saveBar(body,function(m){save(function(b){b.playback={tracearrUrl:tu.value,tracearrKey:tk.value,embyUrl:eu.value,embyKey:ek.value,hiddenLibraries:hl.value,refreshAfterPlace:rf.checked,pathFrom:pf.value,pathTo:pt.value}},m).then(function(){pane(body)})})
  }

  function publicPane(body){
    var D=S.donations||{};
    body.appendChild(head('Public page','What visitors see on the public status page.'));
    var c=el('div','card');var h=el('h4','','Donate button ');h.appendChild(chip(!!(D.paypal_url||D.cashapp_url||D.custom_url),(D.paypal_url||D.cashapp_url||D.custom_url)?'shown':'hidden — no links'));c.appendChild(h);
    var g=el('div','grid3');
    var ti=row(g,'popover title',{value:D.title,ph:'Support this server'});
    var pp=row(g,'PayPal link',{value:D.paypal_url,ph:'https://paypal.me/…'});
    var ca=row(g,'Cash App link',{value:D.cashapp_url,ph:'https://cash.app/$…'});
    var cl=row(g,'other link label',{value:D.custom_label,ph:'Ko-fi'});
    var cu=row(g,'other link URL',{value:D.custom_url,ph:'https://'});
    c.appendChild(g);c.appendChild(help('Links are public. Leave them all blank to hide the Donate button. HTTPS only.'));body.appendChild(c);
    var R=S.requests||{};
    var c2=el('div','card');var h2=el('h4','','Media requests (Seerr) ');h2.appendChild(chip(!!R.enabled,R.enabled?'on':'off'));c2.appendChild(h2);
    var g2=el('div','grid3');
    var re=row(g2,'let visitors request media',{type:'checkbox',value:R.enabled,help:'shows the Request media box on the public page'});
    var ru=row(g2,'Seerr address',{value:R.url,ph:'http://10.0.0.8:5055'});
    var rk=row(g2,'Seerr API key',{type:'password',ph:R.hasApiKey?'set — leave blank to keep':'Seerr → Settings → General',help:'never shown on the public page'});
    var ri=row(g2,'request as user ID',{type:'number',value:R.userId||'',help:'the Seerr user visitors request as; Seerr applies that user’s permissions, quota and auto-approval'});
    c2.appendChild(g2);var rb=el('button','','test');var rm=el('span','msg');rm.style.marginLeft='12px';
    rb.onclick=function(){rm.className='msg';rm.textContent='testing…';api('POST','/requests/test',{url:ru.value,apiKey:rk.value,userId:+ri.value}).then(function(){rm.className='msg ok';rm.textContent='connected, and the key can act as that user'}).catch(function(e){rm.className='msg err';rm.textContent=e.message})};
    c2.appendChild(rb);c2.appendChild(rm);body.appendChild(c2);
    accessCard(body,'settings');
    saveBar(body,function(m){save(function(b){b.requests={enabled:re.checked,url:ru.value,apiKey:rk.value,userId:+ri.value};b.donations={title:ti.value,paypal_url:pp.value,cashapp_url:ca.value,custom_label:cl.value,custom_url:cu.value}},m).then(function(){pane(body)})})
  }

  function accessView(body){accessCard(body,'queue')}
  function accessCard(body,mode){
    var c=el('div','card');var h=el('h4','','Account requests ("need access?") ');var hc=el('span');h.appendChild(hc);c.appendChild(h);
    c.appendChild(help('Visitors ask for a media server account. Approving creates a disabled copy of your template user, and the visitor finishes setup from an emailed single-use link. The media server address and key come from the Media server settings. Emails are sent over TLS/STARTTLS only.'));
    var g=el('div','grid3'),A={};
    var en=row(g,'let visitors request accounts',{type:'checkbox'});
    var pv=row(g,'server type',{help:'emby or jellyfin'});
    var pu=row(g,'this site’s public HTTPS address',{ph:'https://status.example.com',help:'used in the emailed links'});
    var sp=row(g,'media server web address',{ph:'https://emby.example.com',help:'HTTPS. Shown in the “you’re ready to watch” email so people know where to watch. Required for Jellyfin.'});
    var tp=row(g,'template user ID',{help:'a disabled, non-admin user whose permissions new accounts copy'});
    var sh=row(g,'SMTP host',{ph:'smtp.example.com'});
    var sn=row(g,'SMTP port',{type:'number'});
    var ss=row(g,'SMTP security',{help:'tls (port 465) or starttls (587)'});
    var su=row(g,'SMTP username');
    var sw=row(g,'SMTP password',{type:'password'});
    var fr=row(g,'send from',{ph:'undermynd@example.com'});
    var ad=row(g,'admin email',{ph:'you@example.com',help:'told about new requests and problems'});
    c.appendChild(g);
    var msg=el('span','msg');msg.style.marginLeft='12px';
    var sv=el('button','go','save access settings'),tb=el('button','','send test email');
    sv.onclick=function(){msg.className='msg';msg.textContent='saving…';
      api('PUT','/access',{enabled:en.checked,provider:pv.value.trim().toLowerCase(),public_url:pu.value,server_public_url:sp.value,template_id:tp.value.trim(),smtp_host:sh.value.trim(),smtp_port:+sn.value,smtp_security:ss.value.trim().toLowerCase(),smtp_user:su.value,smtp_password:sw.value,from:fr.value.trim(),admin_email:ad.value.trim()})
        .then(function(v){sw.value='';fill(v);msg.className='msg ok';msg.textContent='saved'}).catch(function(e){msg.className='msg err';msg.textContent=e.message})};
    tb.onclick=function(){msg.className='msg';msg.textContent='sending…';api('POST','/access/test-email',{}).then(function(){msg.className='msg ok';msg.textContent='test email sent to the admin address'}).catch(function(e){msg.className='msg err';msg.textContent=e.message})};
    c.appendChild(sv);tb.style.marginLeft='8px';c.appendChild(tb);c.appendChild(msg);
    function fill(v){en.checked=!!v.enabled;pv.value=v.provider||'emby';pu.value=v.public_url||'';sp.value=v.server_public_url||'';tp.value=v.template_id||'';sh.value=v.smtp_host||'';sn.value=v.smtp_port||'';ss.value=v.smtp_security||'';su.value=v.smtp_user||'';fr.value=v.from||'';ad.value=v.admin_email||'';sw.placeholder=v.has_smtp_password?'set — leave blank to keep':'';hc.replaceChildren(chip(!!v.enabled,v.enabled?'on':'off'))}
    body.appendChild(c);
    var q=el('div','card');q.appendChild(el('h4','','Account request queue'));var lst=el('div');q.appendChild(lst);body.appendChild(q);
    var names={pending:'awaiting approval',incomplete:'setup needs attention',provisioning:'creating account',awaiting_setup:'waiting for the visitor to finish setup',awaiting_connect:'approved: waiting for the visitor to confirm Emby\'s email (switches on by itself)',setting_up:'finishing setup',active:'active',declined:'declined'};
    function act(id,a,b,note){b.disabled=true;api('POST','/access/requests/'+id+'/'+a,note||{}).then(loadQ).catch(function(e){b.disabled=false;alert(e.message)})}
    function loadQ(){api('GET','/access/requests').then(function(r){lst.replaceChildren();var items=r.requests||[];if(!items.length){lst.appendChild(help('No account requests yet.'));return}
      items.forEach(function(it){var d=el('div','kv');d.appendChild(el('span','k',it.name+' — '+(names[it.status]||it.status)));d.appendChild(el('span','v',it.email+' · '+it.username+(it.connect_username?' · Connect email '+it.connect_username:'')));
        [it.error,it.mail_error].forEach(function(t){if(t){var e=el('div','msg err',t);d.appendChild(e)}});
        var acts=[];
        if(it.status==='pending')acts=[['approve','approve'],['decline','decline'],['resend','resend notices']];
        else if(it.status==='incomplete')acts=[['approve','retry setup']];
        else if(it.status==='awaiting_setup')acts=[['resend','send fresh setup link']];else if(it.status==='awaiting_connect')acts=[['resend','resend the approval email']];
        else if(it.status==='active')acts=[['resend','resend sign-in instructions']];
        else if(it.status==='declined')acts=[['resend','resend notice']];
        if(it.emby_id&&['incomplete','awaiting_setup','awaiting_connect','active'].indexOf(it.status)>=0)acts.push(['reapply-template','reapply template'],['reset','reset after server deletion']);
        acts.forEach(function(a){var b=el('button','',a[1]);b.onclick=function(){act(it.id,a[0],b)};d.appendChild(b)});
        if(it.status==='pending'||it.status==='incomplete'){var b2=el('button','','correct details');b2.onclick=function(){var em=prompt_('Notification email',it.email);if(em==null)return;var cn=it.connect_username?prompt_('Emby Connect email',it.connect_username):'';if(cn==null)return;b2.disabled=true;api('POST','/access/requests/'+it.id+'/correct',{email:em,connect_username:cn}).then(loadQ).catch(function(e){b2.disabled=false;alert(e.message)})};d.appendChild(b2)}
        lst.appendChild(d)})}).catch(function(e){lst.replaceChildren(help(e.message))})}
    function prompt_(l,v){return window.prompt(l,v)}
    api('GET','/access').then(fill).catch(function(e){msg.className='msg err';msg.textContent=e.message});loadQ();
    if(mode==='queue')c.remove();else q.remove();
  }

  // Downloads › Feeds (pinned v4): feeds with their state, rules, the watch
  // folder, and what the feeds saw with why each item was skipped.
  function rssPane(body){
    var cols=el('div','hv-pipe');var left=el('div','hv-pipelist'),right=el('section','hv-feedsaw');right.setAttribute('aria-label','What the feeds saw');cols.appendChild(left);cols.appendChild(right);body.appendChild(cols);
    var cats=[];api('GET','/settings').then(function(v){cats=(v.categories||[]).map(function(c){return c.name})}).catch(function(){});
    var status={};
    function secHead(title,note,btnLabel,fn){var h=el('div','hv-sechead');h.appendChild(el('h2','',title));if(note)h.appendChild(el('span','hv-note',note));h.appendChild(el('span','hv-sp'));if(btnLabel)h.appendChild(hvBtn(btnLabel,'plus',fn,true));left.appendChild(h)}
    // One editable row: a summary line, and its fields when Edit is open.
    function editRow(host,summary,fields,open,onRemove){var w=el('div','hv-erow');var top=el('div','hv-erow-top');w.appendChild(top);var ed=el('div','hv-erow-ed');ed.hidden=!open;
      var g=el('div','grid3');var f=fields(g);ed.appendChild(g);var rm=hvBtn('Remove','',function(){w.remove();if(onRemove)onRemove()});rm.classList.add('bad');ed.appendChild(rm);w.appendChild(ed);
      function draw(){top.replaceChildren();summary(top,f);var eb=hvBtn(ed.hidden?'Edit':'Done','',function(){ed.hidden=!ed.hidden;draw()});top.appendChild(eb)}
      [].forEach.call(g.querySelectorAll('input'),function(i){i.addEventListener('input',draw);i.addEventListener('change',draw)});draw();w._f=f;host.appendChild(w);return w}
    secHead('Feeds','','Add a feed',function(){addFeed({enabled:true,intervalMinutes:15},true)});
    var feeds=el('div','hv-erows');left.appendChild(feeds);
    function addFeed(c,open){editRow(feeds,function(top,f){var st=status[f.name.value]||{};var on=f.enabled.checked;var bad=!!st.error;
        top.classList.toggle('bad',bad);top.appendChild(el('i','hv-dot2 '+(bad?'bad':on?'ok':'off')));
        var t=el('div','hv-erow-t');t.appendChild(el('b','',f.name.value||'New feed'));
        var when=st.checkedAt&&st.checkedAt.indexOf('0001')!==0?'checked '+ago(st.checkedAt)+' ago':'not checked yet';
        t.appendChild(el('span','',bad?'Last check failed: '+st.error:(on?'every '+(f.intervalMinutes.value||15)+' min · '+when+(st.items?' · '+st.items.length+' items':''):'off')));top.appendChild(t)},
      function(g){var f={};f.name=row(g,'name',{value:c.name});f.url=row(g,'feed address',{value:c.url,help:'the full RSS link, including any key'});
        f.intervalMinutes=row(g,'check every (minutes)',{type:'number',value:c.intervalMinutes||15});f.enabled=row(g,'on',{type:'checkbox',value:c.enabled!==false});return f},open)}
    S.feeds.forEach(function(c){addFeed(c,false)});
    secHead('Rules','What to grab from the feeds','Add a rule',function(){addRule({enabled:true},true)});
    var rules=el('div','hv-erows');left.appendChild(rules);
    function addRule(c,open){editRow(rules,function(top,f){var t=el('div','hv-erow-t');t.appendChild(el('b','',f.name.value||'New rule'));
        var bits=[];if(f.mustContain.value)bits.push('has “'+f.mustContain.value+'”');if(f.mustNotContain.value)bits.push('not “'+f.mustNotContain.value+'”');bits.push('→ '+(f.category.value||'?'));if(f.feed.value)bits.push('only '+f.feed.value);if(!f.enabled.checked)bits.push('off');
        t.appendChild(el('span','hv-mono',bits.join(' · ')));top.appendChild(t)},
      function(g){var f={};f.name=row(g,'rule name',{value:c.name});f.feed=row(g,'only this feed',{value:c.feed,help:'blank = every feed'});
        f.category=row(g,'goes to category',{value:c.category,help:'one of your categories'});
        f.mustContain=row(g,'must contain',{value:c.mustContain,help:'a pattern; case is ignored'});f.mustNotContain=row(g,'must not contain',{value:c.mustNotContain,help:'e.g. cam|hdts'});
        f.enabled=row(g,'on',{type:'checkbox',value:c.enabled!==false});return f},open)}
    S.rules.forEach(function(c){addRule(c,false)});
    left.appendChild(el('p','hv-note','Shows you follow on Library › Anime are handled there, so the rules leave them alone.'));
    secHead('Watch folders','.torrent, .nzb and .magnet files dropped here are downloaded','Add a folder',function(){addWatch({enabled:true},true)});
    var watches=el('div','hv-erows');left.appendChild(watches);
    function addWatch(c,open){editRow(watches,function(top,f){var t=el('div','hv-erow-t');t.appendChild(el('b','hv-mono',f.dir.value||'New folder'));t.appendChild(el('span','','into '+(f.category.value||'the first rule’s category')+(f.enabled.checked?'':' · off')));top.appendChild(t)},
      function(g){var f={};f.dir=row(g,'folder',{value:c.dir,help:'full path as the container sees it'});f.category=row(g,'category',{value:c.category,help:'blank = the category of your first rule'});f.enabled=row(g,'on',{type:'checkbox',value:c.enabled!==false});return f},open)}
    (S.watches||[]).forEach(function(c){addWatch(c,false)});
    saveBar(body,function(m){save(function(b){
      b.watches=[].map.call(watches.children,function(c){var f=c._f;return {dir:f.dir.value,category:f.category.value,enabled:f.enabled.checked}});
      b.feeds=[].map.call(feeds.children,function(c){var f=c._f;return {name:f.name.value,url:f.url.value,intervalMinutes:+f.intervalMinutes.value,enabled:f.enabled.checked}});
      b.rules=[].map.call(rules.children,function(c){var f=c._f;return {name:f.name.value,feed:f.feed.value,mustContain:f.mustContain.value,mustNotContain:f.mustNotContain.value,category:f.category.value,enabled:f.enabled.checked}})},m)});
    // What the feeds saw: grabbed, or why not, with a way to download anyway.
    function manual(feed,idx,title,cell){var btn=hvBtn('Download anyway','dl',function(){
      cell.replaceChildren();var sel=el('select');cats.forEach(function(n){sel.appendChild(new Option(n,n))});var go=hvBtn('Start','',function(){go.disabled=true;msg.textContent='starting…';api('POST','/rss/download',{feed:feed,index:idx,title:title,category:sel.value}).then(function(){showFeeds()}).catch(function(e){go.disabled=false;msg.className='msg err';msg.textContent=e.message})},true);
      var no=hvBtn('Cancel','',function(){cell.replaceChildren(manual(feed,idx,title,cell))});var msg=el('span','msg');[sel,go,no,msg].forEach(function(x){cell.appendChild(x)})});btn.classList.add('sm');return btn}
    function showFeeds(){api('GET','/rss/status').then(function(list){status={};list.forEach(function(st){status[st.name]=st});
      [].forEach.call(feeds.children,function(w){w.querySelector('input').dispatchEvent(new Event('input'))});
      right.replaceChildren();var h=el('div','hv-sechead');h.appendChild(el('h2','','What the feeds saw'));h.appendChild(el('span','hv-sp'));var chk=hvBtn('Check feeds now','refresh',function(){chk.disabled=true;api('POST','/rss/check',{}).then(showFeeds).catch(function(){chk.disabled=false})});h.appendChild(chk);right.appendChild(h);
      var box=el('div','hv-saw');right.appendChild(box);var n=0;
      list.forEach(function(st){(st.items||[]).forEach(function(it,idx){n++;var r=el('div','hv-sawrow');r.appendChild(el('span','hv-sawt',it.title));
        var started=it.outcome.indexOf('started')===0;var o=el('span','hv-sawo '+(started?'ok':''),started?'Grabbed':'Skipped');r.appendChild(o);r.appendChild(el('span','hv-sawwhy',(started?'':it.outcome)+' · '+st.name));
        var cell=el('span','hv-sawact');if(!started)cell.appendChild(manual(st.name,idx,it.title,cell));r.appendChild(cell);box.appendChild(r)})});
      if(!n)box.appendChild(el('div','hv-empty','Nothing read from the feeds yet.'))}).catch(function(e){right.replaceChildren(el('div','err',e.message))})}
    showFeeds();
  }

  function systemPane(body){
    body.appendChild(head('Server','What the pipeline is doing right now, the login the arrs use, and timing.'));
    var libErr=el('div');body.appendChild(libErr);
    api('GET','/library').then(function(d){if(d.errors&&d.errors.length){var c=el('div','card');c.appendChild(el('h4','','Library status problems'));d.errors.slice(0,10).forEach(function(e){c.appendChild(el('div','msg err',e))});libErr.appendChild(c)}}).catch(function(){});
    var live=el('div');body.appendChild(live);
    function bytes(n){if(!n)return '0 B';var u=['B','KB','MB','GB','TB'],i=0;while(n>=1024&&i<4){n/=1024;i++}return n.toFixed(i?1:0)+' '+u[i]}
    function drawSystem(){api('GET','/system').then(function(d){
      live.replaceChildren();var g=el('div','grid3');
      var c1=el('div','card');c1.appendChild(el('h4','','Intake'));var k=el('div','kv');k.appendChild(el('span','k','state'));k.appendChild(chip(!d.paused,d.paused?'paused':'running'));c1.appendChild(k);
      ['queued','downloading','processing','finishing','failed'].forEach(function(x){var r=el('div','kv');r.appendChild(el('span','k',x));r.appendChild(el('span','v',String(d.counts[x]||0)));c1.appendChild(r)});g.appendChild(c1);
      var c2=el('div','card');c2.appendChild(el('h4','','TorBox'));
      if(!d.torbox.configured){c2.appendChild(help('No token set.'))}else{
        [['new usenet downloads this hour',d.torbox.used],['new torrents this hour (cached ones not counted)',d.torbox.torrentUsed||0]].forEach(function(x){var r1=el('div','kv');r1.appendChild(el('span','k',x[0]));r1.appendChild(el('span','v',x[1]+' of '+d.torbox.limit));c2.appendChild(r1);
          var br=el('div','bar');var bf=el('i');bf.style.width=Math.min(100,d.torbox.limit?x[1]*100/d.torbox.limit:0)+'%';br.appendChild(bf);c2.appendChild(br)});
        if(d.torbox.backoffUntil){var r2=el('div','kv');r2.appendChild(el('span','k','backing off until'));r2.appendChild(el('span','v flag',new Date(d.torbox.backoffUntil).toLocaleTimeString()));c2.appendChild(r2)}}
      g.appendChild(c2);
      var c3=el('div','card');c3.appendChild(el('h4','','Tdarr'));var t1=el('div','kv');t1.appendChild(el('span','k','jobs with Tdarr'));t1.appendChild(el('span','v',d.tdarr.inFlight+' of '+d.tdarr.max));c3.appendChild(t1);
      var t2=el('div','kv');t2.appendChild(el('span','k','watched over the API'));t2.appendChild(chip(d.tdarr.api,d.tdarr.api?'yes':'no — folders only'));c3.appendChild(t2);g.appendChild(c3);
      [['Work folder',d.work],['Complete folder',d.complete]].forEach(function(p){var c=el('div','card');c.appendChild(el('h4','',p[0]));var a=el('div','kv');a.appendChild(el('span','k','path'));a.appendChild(el('span','v',p[1].path));c.appendChild(a);
        var b=el('div','kv');b.appendChild(el('span','k','used by HyveMynd'));b.appendChild(el('span','v',bytes(p[1].usedByUs)+(p[1].partial?' (partial count)':'')));c.appendChild(b);
        if(p[1].totalBytes){var f=el('div','kv');f.appendChild(el('span','k','free on this disk'));f.appendChild(el('span','v',bytes(p[1].freeBytes)+' of '+bytes(p[1].totalBytes)));c.appendChild(f)}g.appendChild(c)});
      live.appendChild(g)}).catch(fail)}
    drawSystem();timer=setInterval(function(){if(tab==='system')drawSystem()},5000);
    var c=el('div','card');c.appendChild(el('h4','','Client login for the arrs'));
    c.appendChild(help('Used as the qBittorrent login, and the password is also the SABnzbd API key. It is never shown again; generate a new one to see it once.'));
    var u=el('div','kv');u.appendChild(el('span','k','username'));u.appendChild(el('span','v',S.clientUser));c.appendChild(u);
    var out=el('pre');var gen=el('button','','generate new password');
    gen.onclick=function(){if(!confirm('This replaces the current password. The arrs must be updated.'))return;api('POST','/settings/client-password').then(function(r){out.textContent='username: '+r.user+'\npassword: '+r.password+'\n(shown once)'}).catch(fail)};
    c.appendChild(gen);c.appendChild(out);body.appendChild(c);
    var t=el('div','card');t.appendChild(el('h4','','Timing'));var g=el('div','grid3');
    var st=row(g,'no-progress timeout (minutes)',{type:'number',value:S.stallMinutes,help:'a download with no progress this long is retried'});
    var rc=row(g,'import check interval (seconds)',{type:'number',value:S.reconcileSeconds,help:'how often the arr is asked whether it imported'});
    var fl=row(g,'flag a ready job after (hours)',{type:'number',value:S.flagImportHours,help:'a job the arr has not imported for this long is flagged'});
    var sc=row(g,'storage scan interval (minutes)',{type:'number',value:S.storageScanMinutes,help:'how often library sizes are measured in the background; the last result is saved and shown right away after a restart'});
    var qc=row(g,'check resolution against the arr',{type:'checkbox',value:S.qualityCheck!==false,help:'a download whose real video size is below the quality the arr grabbed (a 1080p release that is really 480p) fails, so the arr blocklists it and searches again'});
    t.appendChild(g);body.appendChild(t);
    saveBar(body,function(m){save(function(b){b.qualityCheck=qc.checked;b.stallMinutes=+st.value;b.reconcileSeconds=+rc.value;b.flagImportHours=+fl.value;b.storageScanMinutes=+sc.value},m)})
  }
  start();
})();
