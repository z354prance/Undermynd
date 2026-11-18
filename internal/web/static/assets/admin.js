(function(){
  var root=document.getElementById('root'),logoutBtn=document.getElementById('logout'),timer=null;
  function el(t,c,x){var e=document.createElement(t);if(c)e.className=c;if(x!=null)e.textContent=x;return e}
  function api(method,path,body){
    return fetch('/admin/api'+path,{method:method,headers:{'Content-Type':'application/json','X-Undermynd-Request':'1'},body:body?JSON.stringify(body):undefined,credentials:'same-origin'})
      .then(function(r){return r.json().catch(function(){return {}}).then(function(d){if(!r.ok){var e=new Error(d.error||('HTTP '+r.status));e.status=r.status;throw e}return d})});
  }
  function field(form,label,type,name,ph){form.appendChild(el('label','',label));var i=el('input');i.type=type||'text';i.name=name;if(ph)i.placeholder=ph;i.autocomplete=type==='password'?'current-password':'off';form.appendChild(i);return i}
  function prompt(cmd){var d=el('div');d.appendChild(el('span','prompt','~/undermynd '));var b=el('b','','❯');b.style.color='var(--teal)';b.style.fontWeight='400';d.appendChild(b);d.appendChild(el('span','cmd',' '+cmd));return d}
  function start(){
    stopLive();logoutBtn.hidden=true;document.body.classList.remove('wide');
    api('GET','/session').then(function(s){
      if(s.signedIn)return app();
      if(s.needsSetup)return setup();
      if(s.pending)return confirmStep();
      login();
    }).catch(function(e){root.replaceChildren(el('div','err',e.message))});
  }
  function login(){
    root.replaceChildren(prompt('sign-in'));
    var f=el('form','form');var p=field(f,'password','password','password');var c=field(f,'authenticator code (or a recovery code)','text','code');c.autocomplete='one-time-code';
    var b=el('button','go','sign in');b.type='submit';f.appendChild(el('div'));f.appendChild(b);var err=el('div','err');f.appendChild(err);
    f.onsubmit=function(e){e.preventDefault();err.textContent='';api('POST','/login',{password:p.value,code:c.value}).then(start).catch(function(x){err.textContent=x.message})};
    root.appendChild(f);p.focus();
  }
  var pendingPw='';
  function setup(){
    root.replaceChildren(prompt('setup'));
    root.appendChild(el('p','hint','First run. The setup key is printed in the container log (also in the config folder as setup-key).'));
    var f=el('form','form');var k=field(f,'setup key','text','key');var p=field(f,'choose a password (12+ characters)','password','password');p.autocomplete='new-password';
    var b=el('button','go','create account');b.type='submit';f.appendChild(el('div'));f.appendChild(b);var err=el('div','err');f.appendChild(err);
    f.onsubmit=function(e){e.preventDefault();err.textContent='';api('POST','/setup',{key:k.value,password:p.value}).then(function(r){pendingPw=p.value;confirmStep(r)}).catch(function(x){err.textContent=x.message})};
    root.appendChild(f);k.focus();
  }
  function confirmStep(r){
    root.replaceChildren(prompt('setup --authenticator'));
    if(r){
      root.appendChild(el('p','hint','Add this secret to an authenticator app (any TOTP app), then enter the 6-digit code it shows.'));
      root.appendChild(el('pre','',r.totpSecret));
      root.appendChild(el('div','hint','or open this link on your phone: '));
      var a=el('a','',r.otpauthUri);a.href=r.otpauthUri;root.appendChild(a);
    }else{root.appendChild(el('p','hint','Setup was started but not finished. Enter your password and the code from your authenticator.'))}
    var f=el('form','form');var p=null;if(!pendingPw)p=field(f,'password','password','password');var c=field(f,'code from the app','text','code');c.autocomplete='one-time-code';
    var b=el('button','go','confirm');b.type='submit';f.appendChild(el('div'));f.appendChild(b);var err=el('div','err');f.appendChild(err);
    f.onsubmit=function(e){e.preventDefault();err.textContent='';api('POST','/setup/confirm',{password:pendingPw||p.value,code:c.value}).then(function(d){pendingPw='';
      root.replaceChildren(prompt('recovery-codes'));root.appendChild(el('p','hint','Save these now. Each works once if you lose your authenticator. They are not shown again.'));
      root.appendChild(el('pre','',d.recovery.join('\n')));var go=el('button','go','continue to sign in');go.onclick=start;root.appendChild(go)}).catch(function(x){err.textContent=x.message})};
    root.appendChild(f);c.focus();
  }
  // ---------------------------------------------------------------- app
  var tab='overview',detailId=null;
  function app(){
    logoutBtn.hidden=false;logoutBtn.onclick=function(){api('POST','/logout').then(start)};
    root.replaceChildren();
    document.body.classList.add('wide');
    var shell=el('div','shell'),side=el('nav','side');
    var brand=el('a','brand');brand.href='/admin';var bi=el('img');bi.src='/assets/logo.svg';bi.alt='';brand.appendChild(bi);brand.appendChild(el('span','',  '~/undermynd'));side.appendChild(brand);
    function nav(t,label){var b=el('button',t===tab?'on':'',label);tabBtns[t]=b;b.onclick=function(){stopLive();tab=t;detailId=null;if(t==='anime'){aView.page='list'}app()};side.appendChild(b)}
    nav('overview','Overview');nav('jobs','Jobs');nav('requests','Requests');nav('anime','Anime');nav('indexers','Indexers');nav('library','Library');nav('reencode','Re-encode');
    [['Connections',[['arrs','Arrs & categories'],['clients','Download clients'],['transcode','Transcode'],['metadata','Metadata']]],['Public site',[['playback','Playback'],['public','Public page']]],['Automation',[['rss','Feeds']]],['Server',[['system','Settings'],['log','Log']]]].forEach(function(g){
      side.appendChild(el('div','sgroup',g[0]));
      g[1].forEach(function(p){nav(p[0],p[1])})});
    side.appendChild(el('span','sp'));var pub=el('a','sidelink','public page');pub.href='/';side.appendChild(pub);
    var so=el('button','sidelink','sign out');so.onclick=function(){api('POST','/logout').then(start)};side.appendChild(so);
    var main=el('main','main'+(tab==='anime'||tab==='indexers'?' arr-main':'')),body=el('div');main.appendChild(body);shell.appendChild(side);shell.appendChild(main);root.appendChild(shell);
    if(tab==='overview')overviewView(body);else if(tab==='jobs')jobsView(body);else if(tab==='requests')requestsView(body);else if(tab==='library')libraryView(body);else if(tab==='anime')animeView(body);else if(tab==='indexers')indexersView(body);else if(tab==='reencode')reencodeView(body);else if(tab==='log')logView(body);else settingsView(body);
  }
  function fail(e){if(e.status===401){start()}else{var d=el('div','err',e.message);root.appendChild(d)}}
  function ago(t){var s=Math.max(0,(Date.now()-new Date(t))/1000);if(s<90)return Math.round(s)+'s';if(s<5400)return Math.round(s/60)+'m';if(s<172800)return Math.round(s/3600)+'h';return Math.round(s/86400)+'d'}
  var jobFilter={q:'',group:'all',source:'all'},lastRows=[],paused=false,es=null,tabBtns={};
  function stopLive(){clearInterval(timer);if(es){es.close();es=null}}
  function attentionCount(rows){return rows.filter(function(r){return r.attention&&r.state!=='cleaned'}).length}
  var WTORBOX=['Requested','Found','Download','Transfer','Convert','Ready'],WLOCAL=['Requested','Found','Download','Convert','Ready'];
  function jobWire(j){
    // A local downloader writes straight to the server: no Transfer step.
    var tb=j.backend==='torbox',WSTAGES=tb?WTORBOX:WLOCAL,ix=function(n){return WSTAGES.indexOf(n)};
    var fa={download:'Download',transfer:tb?'Transfer':'Download',convert:'Convert',ready:'Ready'}[j.failedAt]||'Download';
    var m={received:[0,'wait'],submitted:[1,'wait'],downloading:[ix('Download'),'run'],pulling:[ix(tb?'Transfer':'Download'),'run'],downloaded:[ix('Convert'),'wait'],queued_transcode:[ix('Convert'),'wait'],transcoding:[ix('Convert'),'run'],verifying:[ix('Convert'),'run'],ready:[ix('Ready'),'wait'],imported:[WSTAGES.length,'done'],cleaned:[WSTAGES.length,'done'],failed:[ix(fa),'fail']}[j.state]||[1,'wait'];
    var cur=m[0],mode=m[1],pct=Math.max(0,Math.min(100,Math.round((j.progress||0)*100)));
    if(mode==='run'&&pct===0)mode='wait';
    var w=el('div','wire w-'+mode);
    WSTAGES.forEach(function(name,i){
      var st=el('div','st '+(cur>i?'done':cur===i?'cur':'todo'));var wr=el('div','wr');wr.appendChild(el('i','nd'));
      if(i<WSTAGES.length-1){
        var ln=el('b','ln'+(cur>i?'':cur===i?' act':' todo'));
        if(cur===i){var p=(mode==='run'||mode==='fail')?pct:0;ln.style.setProperty('--p',p+'%');var f=el('s','fill');f.style.width=p+'%';f.style.backgroundSize=(p>0?Math.round(10000/p):100)+'% 100%';ln.appendChild(f);if(mode==='run'&&p>0){var d=el('u','dot');d.style.left=p+'%';ln.appendChild(d)}ln.appendChild(el('em','rest'))}
        wr.appendChild(ln)}
      st.appendChild(wr);st.appendChild(el('span','lb',name));w.appendChild(st)});
    return w}
  function jobActions(j){
    var act=el('div','acts');
    function b(cls,label,fn,title){var x=el('button',cls,label);if(title)x.title=title;x.onclick=fn;act.appendChild(x)}
    if(j.state==='failed'){b('go','retry',function(){doAction(j.id,'retry')});b('','clean up now',function(){doAction(j.id,'cleanup')})}
    if(j.state==='ready')b('go','mark imported',function(){doAction(j.id,'imported')});
    if(j.state==='imported')b('','clean up now',function(){doAction(j.id,'cleanup')});
    if(j.state!=='cleaned'&&j.state!=='failed'&&j.state!=='imported')b('','fail it',function(){if(confirm('Fail this job? The arr will treat the download as failed and may search again.'))doAction(j.id,'fail')},'report a failed download so the arr blocklists this release and grabs another');
    if(j.state!=='cleaned')b('bad','cancel',function(){if(confirm('Cancel and clean up this job?'))doAction(j.id,'cancel')});
    return act}
  function jobRow(j,onOpen){
    var r=el('div','jrow'+(j.state==='failed'?' failed':''));
    var n=el('div','jname');var a=el('button','link',j.name);a.onclick=function(){onOpen(j)};n.appendChild(a);
    var why=(j.flag?j.flag+' ':'')+(j.reason&&j.state!=='cleaned'?j.reason:'');
    n.appendChild(el('div','reason',why||(j.source+' / '+j.category+' · '+ago(j.created)+' ago')));
    if(why)n.appendChild(el('div','reason faintline',j.source+' / '+j.category+' · '+(j.backend||'-')+' · '+ago(j.created)+' ago'+(j.transcodeNode?' · '+j.transcodeNode+(j.transcodeWorker?' / '+j.transcodeWorker:''):'')));
    r.appendChild(n);var w=el('div','jwire');w.appendChild(jobWire(j));r.appendChild(w);r.appendChild(jobActions(j));return r}
  var WORKING={queued:1,downloading:1,processing:1,finishing:1};
  function jobsView(body){
    body.appendChild(el('h2','ptitle','Jobs'));
    var bar=el('div','toolbar');var pills=el('div','pills');bar.appendChild(pills);
    var q=el('input');q.placeholder='search title';q.value=jobFilter.q;q.oninput=function(){jobFilter.q=q.value;drawRows()};bar.appendChild(q);
    var ss=el('select');[['all','all sources'],['sonarr','sonarr'],['radarr','radarr'],['lidarr','lidarr'],['rss','rss']].forEach(function(x){var o=el('option','',x[1]);o.value=x[0];if(jobFilter.source===x[0])o.selected=true;ss.appendChild(o)});ss.onchange=function(){jobFilter.source=ss.value;drawRows()};bar.appendChild(ss);
    bar.appendChild(el('span','sp'));
    var pb=el('button','','');function pauseLabel(){pb.textContent=paused?'resume intake':'pause intake';pb.className=paused?'go':''}
    pb.onclick=function(){api('POST','/intake',{paused:!paused}).then(function(r){paused=r.paused;pauseLabel();banner()}).catch(fail)};pauseLabel();bar.appendChild(pb);
    body.appendChild(bar);var note=el('div','notice');body.appendChild(note);var host=el('div');body.appendChild(host);
    function banner(){note.textContent=paused?'Intake is paused: new grabs are accepted but held until you resume.':'';note.style.display=paused?'block':'none';pauseLabel()}
    function inGroup(r,g){
      if(g==='attention')return r.attention&&r.state!=='cleaned';
      if(g==='working')return !!WORKING[r.group];
      if(g==='done')return r.group==='done';
      return r.state!=='cleaned'}
    function match(r){
      if(jobFilter.q&&r.name.toLowerCase().indexOf(jobFilter.q.toLowerCase())<0)return false;
      if(jobFilter.source!=='all'&&r.source!==jobFilter.source)return false;
      return inGroup(r,jobFilter.group)}
    function drawRows(){
      if(tab!=='jobs'||detailId)return;
      var base=lastRows.filter(function(r){return (!jobFilter.q||r.name.toLowerCase().indexOf(jobFilter.q.toLowerCase())>=0)&&(jobFilter.source==='all'||r.source===jobFilter.source)});
      pills.replaceChildren();
      [['all','All'],['attention','Needs you'],['working','Working'],['done','Done']].forEach(function(g){
        var n=base.filter(function(r){return inGroup(r,g[0])}).length;
        var p=el('button','fpill '+g[0]+(jobFilter.group===g[0]?' on':''),g[1]+' '+n);p.onclick=function(){jobFilter.group=g[0];drawRows()};pills.appendChild(p)});
      var list=lastRows.filter(match);host.replaceChildren();
      var ac=attentionCount(lastRows);if(tabBtns.jobs){tabBtns.jobs.textContent='Jobs';if(ac){var bd=el('span','badge',String(ac));bd.title=ac+' need attention';tabBtns.jobs.appendChild(bd)}}
      if(!list.length){host.appendChild(el('div','empty',lastRows.length?'nothing matches these filters':'no jobs yet'));return}
      var box=el('div','jlist');list.forEach(function(j){box.appendChild(jobRow(j,function(j){detailId=j.id;detail(body,j.id)}))});host.appendChild(box);
    }
    function take(d){lastRows=d.jobs||[];paused=!!d.paused;banner();drawRows()}
    function poll(){api('GET','/jobs').then(function(rows){lastRows=rows;drawRows()}).catch(fail)}
    if(window.EventSource){
      es=new EventSource('/admin/api/events');
      es.addEventListener('jobs',function(e){try{take(JSON.parse(e.data))}catch(x){}});
      es.onerror=function(){if(es&&es.readyState===2){es.close();es=null;poll();timer=setInterval(poll,3000)}};
    }else{poll();timer=setInterval(poll,3000)}
    banner();drawRows();
  }

  function loadReqs(){return api('GET','/requests').then(function(d){return d.requests||[]}).catch(function(){return null})}
  function decideReq(r,action,done){api('POST','/requests/'+encodeURIComponent(r.source_id)+'/'+action).then(done).catch(function(e){if(e.status===401)fail(e);else{alert(e.message);done()}})}
  function reqRow(r,done,compact){
    var row=el('div',compact?'orow rrow':'jrow rrow');
    var n=el('div','jname');n.appendChild(el('div','',(r.title||'Untitled request')));
    var who=[r.media_type==='tv'?'TV show':'Movie',r.requested_by,r.seasons&&r.seasons.length?'seasons '+r.seasons.join(', '):''].filter(Boolean).join(' · ');
    n.appendChild(el('div','reason',who));row.appendChild(n);
    var a=el('div','acts rhoriz');var ap=el('button','go','Approve');ap.onclick=function(){ap.disabled=true;decideReq(r,'approve',done)};var dc=el('button','','Decline');dc.onclick=function(){dc.disabled=true;decideReq(r,'decline',done)};a.appendChild(ap);a.appendChild(dc);row.appendChild(a);return row}
  function setReqBadge(n){if(!tabBtns.requests)return;tabBtns.requests.textContent='Requests';if(n){var bd=el('span','badge',String(n));tabBtns.requests.appendChild(bd)}}
  function logView(body){
    body.appendChild(el('h2','ptitle','Log'));
    body.appendChild(el('p','hint','The container log since Undermynd last started, newest at the bottom. Type a job id (the short code after "job"), or words like tdarr or failed, to narrow it. The last 2000 lines are kept.'));
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
  function requestsView(body){
    body.appendChild(el('h2','ptitle','Requests'));var host=el('div');host.style.marginTop='18px';body.appendChild(host);
    function draw(){loadReqs().then(function(list){host.replaceChildren();
      if(list===null){host.appendChild(el('div','empty','Requests are not set up, or Seerr could not be reached. Set it under Public page.'));return}
      setReqBadge(list.length);
      if(!list.length){host.appendChild(el('div','empty','Nothing is waiting for approval.'));return}
      var box=el('div','jlist');list.forEach(function(r){box.appendChild(reqRow(r,draw,false))});host.appendChild(box)})}
    draw();timer=setInterval(draw,15000)}
  // ---- Anime: followed show folders, arr-style (roadmap C3) ----
  var aView={page:'list',folder:null,filter:'all',q:''};
  var IC={refresh:'<path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/>',search:'<circle cx="11" cy="11" r="7"/><path d="M20 20l-4-4"/>',user:'<circle cx="12" cy="8" r="4"/><path d="M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6"/>',plus:'<path d="M12 5v14M5 12h14"/>',edit:'<path d="M4 20h4L19 9l-4-4L4 16v4z"/>',filter:'<path d="M3 5h18l-7 8v6l-4 2v-8L3 5z"/>',book:'<path d="M6 3h12v18l-6-4-6 4V3z" fill="currentColor"/>',bookoff:'<path d="M6 3h12v18l-6-4-6 4V3z"/>',dl:'<path d="M12 4v11M7 10l5 5 5-5M5 20h14"/>',x:'<path d="M6 6l12 12M18 6L6 18"/>',warn:'<path d="M12 3l10 18H2L12 3z"/><path d="M12 10v5M12 18h.01"/>',folder:'<path d="M3 6h7l2 2h9v11H3z"/>',drive:'<rect x="3" y="12" width="18" height="7" rx="1"/><path d="M5 12l3-7h8l3 7"/>',rss:'<path d="M5 19h.01M5 12a7 7 0 0 1 7 7M5 5a14 14 0 0 1 14 14"/>',up:'<path d="M12 19V5M6 11l6-6 6 6"/>',down:'<path d="M12 5v14M6 13l6 6 6-6"/>',back:'<path d="M15 6l-6 6 6 6"/>',cal:'<rect x="3" y="5" width="18" height="16" rx="1.5"/><path d="M3 10h18M8 3v4M16 3v4"/>'};
  function svg(n,label){var s='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" '+(label?'role="img" aria-label="'+label+'"':'aria-hidden="true"')+'>'+IC[n]+'</svg>';var w=el('span','a-ic');w.innerHTML=s;return w}
  function aBtn(icon,label,fn,cls){var b=el('button','a-tb'+(cls?' '+cls:''));b.type='button';b.appendChild(svg(icon));b.appendChild(el('span','',label));b.onclick=function(){fn(b)};return b}
  function aIcon(icon,label,fn){var b=el('button','a-iconbtn');b.type='button';b.setAttribute('aria-label',label);b.title=label;b.appendChild(svg(icon));b.onclick=function(e){e.stopPropagation();fn(b)};return b}
  function aLbl(text,cls){return el('span','a-lbl'+(cls?' '+cls:''),text)}
  function aSize(b){if(!b)return '';var u=['B','KB','MB','GB','TB'],i=0;while(b>=1024&&i<4){b/=1024;i++}return b.toFixed(i>2?1:0)+' '+u[i]}
  function aBar(sh){var s=sh.summary,tot=s.files+s.gaps,have=s.files,cls=s.gaps?' gap':(sh.anilist&&sh.anilist.status==='FINISHED'?' done':'');
    var b=el('div','a-bar'+cls);b.setAttribute('role','img');b.setAttribute('aria-label',have+' of '+tot+' on disk');var i=el('i');i.style.width=(tot?100*have/tot:0)+'%';b.appendChild(i);b.appendChild(el('span','',have+' / '+tot));return b}
  function aStatus(sh){var a=sh.anilist;if(!a)return '';var m={RELEASING:'Airing',FINISHED:'Ended',NOT_YET_RELEASED:'Not aired yet',CANCELLED:'Cancelled',HIATUS:'On hiatus'}[a.status]||a.status;
    if(a.status==='RELEASING'&&a.nextAt){var d=Math.round((a.nextAt*1000-Date.now())/86400000);m+=' · ep '+a.nextEpisode+(d<=0?' today':d===1?' tomorrow':' in '+d+'d')}if(a.episodes)m+=' · '+a.episodes+' eps';return m}
  function aGroups(gs){var w=el('span','a-grp');(gs||[]).forEach(function(g,i){if(i)w.appendChild(el('span','a-arrow','›'));w.appendChild(aLbl(g))});return w}
  function aToolbar(items){var t=el('div','a-toolbar');t.setAttribute('role','toolbar');items.forEach(function(x){if(x==='|')t.appendChild(el('div','a-sep'));else if(x==='>')t.appendChild(el('div','a-grow'));else t.appendChild(x)});return t}
  function aMsg(){return el('div','a-msg')}
  function setMsg(m,text,bad){m.className='a-msg'+(bad?' bad':' ok');m.textContent=text}
  function animeView(body){var w=el('div','arr');body.appendChild(w);
    if(aView.page==='show')return aShowPage(w,aView.folder);if(aView.page==='add')return aAddPage(w);aListPage(w)}
  function aListPage(w){
    var msg=aMsg(),host=el('div','a-content');
    w.appendChild(aToolbar([aBtn('refresh','Rescan folders',function(b){b.disabled=true;api('POST','/shows/rescan',{}).then(function(){setTimeout(app,600)}).catch(function(e){b.disabled=false;setMsg(msg,e.message,true)})}),'|',
      aBtn('plus','Add New',function(){aView.page='add';app()}),'>',
      aBtn('filter',aView.filter==='following'?'Following':'All shows',function(){aView.filter=aView.filter==='following'?'all':'following';app()},aView.filter==='following'?'on':'')]));
    w.appendChild(host);host.appendChild(msg);
    var q=el('input','a-input');q.type='search';q.placeholder='Filter shows';q.value=aView.q;q.setAttribute('aria-label','filter shows');host.appendChild(q);
    var box=el('div','a-tw');host.appendChild(box);var note=el('div','a-dim a-small');host.appendChild(note);
    api('GET','/shows').then(function(d){
      if(!d.roots.length){box.appendChild(el('div','a-empty','No library folder is set. Give your anime category a library folder under Arrs & categories.'));return}
      function draw(){var list=d.shows.filter(function(s){return (aView.filter!=='following'||s.followed)&&(!aView.q||s.name.toLowerCase().indexOf(aView.q.toLowerCase())>=0||(s.titles||[]).join(' ').toLowerCase().indexOf(aView.q.toLowerCase())>=0)});
        box.replaceChildren();var t=el('table','a-t'),hr=el('tr');['','Show','Groups (in order)','Quality','Episodes on disk','Gaps','Latest','Status',''].forEach(function(h){hr.appendChild(el('th','',h))});var th=el('thead');th.appendChild(hr);t.appendChild(th);var tb=el('tbody');
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
        note.textContent=d.shows.length+' folders · '+fol+' followed · '+gaps+' gaps · showing '+Math.min(list.length,400)+'. The bar is episodes on disk out of the numbers from 1 to the newest; orange means numbers are missing in between. Status comes from AniList once a show has been opened or followed.'}
      q.oninput=function(){aView.q=q.value;draw()};draw();
      if(d.scan&&d.scan.running){setMsg(msg,'Reading folders… '+d.scan.done+' of '+(d.scan.total||'?')+'. The list fills in as it goes.');clearInterval(timer);timer=setInterval(function(){if(tab!=='anime'||aView.page!=='list'){clearInterval(timer);return}api('GET','/shows').then(function(x){d.shows=x.shows;d.active=x.active;draw();if(x.scan.running)setMsg(msg,'Reading folders… '+x.scan.done+' of '+(x.scan.total||'?')+'.');else{clearInterval(timer);msg.textContent=''}}).catch(function(){})},3000)}
    }).catch(function(e){if(e.status===401)fail(e);else setMsg(msg,e.message,true)})}
  function aFollow(s,after){var on=!!s.followed;var b=el('button','a-mon'+(on?'':' off'));b.type='button';b.setAttribute('aria-label',(on?'following: ':'not following: ')+s.name);b.title=on?'Following: click to stop':'Not following: click to follow';b.appendChild(svg(on?'book':'bookoff'));
    b.onclick=function(e){e.stopPropagation();b.disabled=true;api('PUT','/shows/one',{folder:s.folder,followed:!on,groups:s.groups,quality:s.quality||'1080p',titles:s.titles,fromEpisode:on?s.fromEpisode:0,anilistId:s.anilistId||0,fillGaps:s.followed?!!s.fillGaps:true}).then(after).catch(function(err){b.disabled=false;alert(err.message)})};return b}
  function aShowPage(w,folder){
    var msg=aMsg();var host=el('div');w.appendChild(host);
    api('GET','/shows/one?folder='+encodeURIComponent(folder)).then(function(d){var s=d.show;
      function act(b,path,body,done){b.disabled=true;setMsg(msg,'working…');api('POST','/shows/'+path,body).then(function(r){b.disabled=false;done(r)}).catch(function(e){b.disabled=false;setMsg(msg,e.message,true)})}
      host.appendChild(aToolbar([aBtn('back','Library',function(){aView.page='list';app()}),'|',aBtn('refresh','Rescan folder',function(){app()}),
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
  function indexersView(body){var w=el('div','arr');body.appendChild(w);var msg=aMsg();
    w.appendChild(aToolbar([aBtn('plus','Add indexer',function(){idxEdit({kind:'torznab',enabled:true,priority:25},function(){app()})}),aBtn('dl','Import from Prowlarr',function(){idxImport(function(){app()})}),'>']));
    var c=el('div','a-content');w.appendChild(c);c.appendChild(msg);
    var sb=el('div','a-row');var q=el('input','a-input');q.type='search';q.placeholder='Search every indexer';q.value=iView.q;q.setAttribute('aria-label','search every indexer');
    var ty=el('select','a-input');ty.setAttribute('aria-label','kind of search');ty.style.maxWidth='160px';[['search','anything'],['tv','TV'],['movie','movies'],['music','music']].forEach(function(o){var op=new Option(o[1],o[0]);op.selected=iView.type===o[0];ty.appendChild(op)});
    var go=el('button','a-btn primary','Search');go.type='button';sb.appendChild(q);sb.appendChild(ty);sb.appendChild(go);c.appendChild(sb);var res=el('div');c.appendChild(res);
    var listBox=el('div','a-tw');c.appendChild(el('h3','a-dim','Indexers'));c.appendChild(listBox);
    var cats=[];
    api('GET','/indexers').then(function(d){cats=d.categories||[];if(!d.indexers.length){listBox.appendChild(el('div','a-empty','No indexers yet. Import them from Prowlarr, or add one by its Torznab or Newznab address.'));return}
      var t=el('table','a-t'),hr=el('tr');['Name','Kind','Priority','Status','Today','',''].forEach(function(h){hr.appendChild(el('th','',h))});var th=el('thead');th.appendChild(hr);t.appendChild(th);var tb=el('tbody');
      d.indexers.forEach(function(i){var tr=el('tr');var n=el('td');n.appendChild(el('div','',i.name));n.appendChild(el('div','a-dim a-small',i.url.replace(/apikey=[^&]+/,'apikey=…')));tr.appendChild(n);
        var k=el('td');k.appendChild(aLbl(i.kind==='newznab'?'usenet':'torrent'));if(i.via)k.appendChild(aLbl('via '+i.via,'orange'));tr.appendChild(k);tr.appendChild(el('td','',String(i.priority)));
        var st=el('td');var resting=i.disabledUntil&&i.disabledUntil[0]!=='0'&&new Date(i.disabledUntil)>new Date();
        st.appendChild(!i.enabled?aLbl('off'):resting?aLbl('resting','orange'):i.lastError?aLbl('error','red'):aLbl('ok','green'));if(i.lastError)st.appendChild(el('div','a-dim a-small',i.lastError));tr.appendChild(st);
        tr.appendChild(el('td','a-dim',i.queriesToday+' searches'+(i.queryLimit?' / '+i.queryLimit:'')+' · '+i.grabsToday+' grabs'+(i.grabLimit?' / '+i.grabLimit:'')));
        var a=el('td','a-acts');a.appendChild(aIcon('refresh','test '+i.name,function(b){b.disabled=true;api('POST','/indexers/test',i).then(function(cp){b.disabled=false;setMsg(msg,i.name+': ok'+(cp.server?' ('+cp.server+')':'')+(cp.tvSearch?' · TV search':'')+(cp.movieSearch?' · movie search':''))}).catch(function(e){b.disabled=false;setMsg(msg,i.name+': '+e.message,true)})}));
        a.appendChild(aIcon('edit','edit '+i.name,function(){idxEdit(i,function(){app()})}));a.appendChild(aIcon('x','delete '+i.name,function(){if(confirm('Delete '+i.name+'?'))api('DELETE','/indexers/'+i.id).then(function(){app()})}));tr.appendChild(a);tb.appendChild(tr)});
      t.appendChild(tb);listBox.appendChild(t)}).catch(function(e){if(e.status===401)fail(e);else setMsg(msg,e.message,true)});
    function run(){iView.q=q.value;iView.type=ty.value;if(!q.value.trim())return;res.replaceChildren(el('div','a-dim','Searching every indexer…'));
      api('GET','/indexers/search?q='+encodeURIComponent(q.value)+'&type='+ty.value).then(function(r){res.replaceChildren();
        (r.errors||[]).concat(r.skipped||[]).forEach(function(x){res.appendChild(el('div','a-dim a-small',x.indexer+': '+x.reason))});
        if(!r.results.length){res.appendChild(el('div','a-empty','Nothing found.'));return}
        var t=el('table','a-t'),hr=el('tr');['Indexer','Age','Title','Size','Peers',''].forEach(function(h){hr.appendChild(el('th','',h))});var th=el('thead');th.appendChild(hr);t.appendChild(th);var tb=el('tbody');
        r.results.slice(0,300).forEach(function(x){var tr=el('tr');var ic=el('td');ic.appendChild(aLbl(x.indexer));tr.appendChild(ic);tr.appendChild(el('td','a-dim',x.published&&x.published[0]!=='0'?ago(x.published):''));tr.appendChild(el('td','a-mono',x.title));tr.appendChild(el('td','a-dim',aSize(x.size)));tr.appendChild(el('td','a-dim',x.protocol==='torrent'?String(x.seeders)+' / '+x.peers:'usenet'));
          var g=el('td','a-acts');var sel=el('select','a-input');sel.style.maxWidth='150px';sel.setAttribute('aria-label','category');cats.forEach(function(cn){sel.appendChild(new Option(cn,cn))});if(iView.cat)sel.value=iView.cat;g.appendChild(sel);
          var b=el('button','a-dl');b.type='button';b.setAttribute('aria-label','grab '+x.title);b.appendChild(svg('dl'));b.onclick=function(){iView.cat=sel.value;b.disabled=true;var body=Object.assign({},x,{category:sel.value});api('POST','/indexers/grab',body).then(function(){b.classList.add('done');setMsg(msg,'Started '+x.title+' in '+sel.value+'.')}).catch(function(e){b.disabled=false;setMsg(msg,e.message,true)})};g.appendChild(b);tr.appendChild(g);tb.appendChild(tr)});
        t.appendChild(tb);var tw=el('div','a-tw');tw.appendChild(t);res.appendChild(tw)}).catch(function(e){res.replaceChildren();setMsg(msg,e.message,true)})}
    go.onclick=run;q.onkeydown=function(e){if(e.key==='Enter')run()};
  }
  function idxEdit(i,after){var md=aModal(i.id?'Edit · '+i.name:'Add indexer','860px');var f=aForm();md.body.appendChild(f.el);
    var nm=el('input','a-input');nm.id='in';nm.value=i.name||'';f.row('Name',nm,null,'in');
    var kd=el('select','a-input');kd.id='ik';[['torznab','torrents (Torznab)'],['newznab','usenet (Newznab)']].forEach(function(o){var op=new Option(o[1],o[0]);op.selected=i.kind===o[0];kd.appendChild(op)});f.row('Kind',kd,null,'ik');
    var ur=el('input','a-input');ur.id='iu';ur.value=i.url||'';ur.placeholder='https://indexer.example/api';f.row('Address',ur,'The Torznab or Newznab API address; /api is added when missing.','iu');
    var ky=el('input','a-input');ky.id='iy';ky.type='password';ky.placeholder=i.hasKey?'set; leave blank to keep':'';f.row('API key',ky,null,'iy');
    var pr=el('input','a-input');pr.id='ip';pr.type='number';pr.min='1';pr.max='50';pr.value=String(i.priority||25);f.row('Priority',pr,'1 is asked first. Like Prowlarr: 1 to 50.','ip');
    var ct=el('input','a-input');ct.id='ic';ct.value=(i.categories||[]).join(', ');ct.placeholder='e.g. 5070 for anime';f.row('Categories',ct,'Newznab category numbers to search; empty uses what each search asks for.','ic');
    var ql=el('input','a-input');ql.id='iq';ql.type='number';ql.min='0';ql.value=String(i.queryLimit||0);f.row('Searches a day',ql,'0 is no limit.','iq');
    var gl=el('input','a-input');gl.id='ig';gl.type='number';gl.min='0';gl.value=String(i.grabLimit||0);f.row('Grabs a day',gl,'0 is no limit.','ig');
    var enl=el('label','a-check');var en=el('input');en.type='checkbox';en.checked=i.enabled!==false;enl.appendChild(en);enl.appendChild(el('span','','Use this indexer'));f.row('On',enl);
    var msg=aMsg();md.body.appendChild(msg);function val(){return {id:i.id||0,name:nm.value,kind:kd.value,url:ur.value,apiKey:ky.value,priority:+pr.value||25,categories:ct.value.split(/[,\s]+/).filter(Boolean).map(Number).filter(function(n){return n>0}),queryLimit:+ql.value||0,grabLimit:+gl.value||0,enabled:en.checked,via:i.via||''}}
    var tb=el('button','a-btn','Test');tb.type='button';tb.onclick=function(){setMsg(msg,'testing…');api('POST','/indexers/test',val()).then(function(cp){setMsg(msg,'ok'+(cp.server?': '+cp.server:''))}).catch(function(e){setMsg(msg,e.message,true)})};
    var cn=el('button','a-btn','Cancel');cn.type='button';cn.onclick=md.close;var sv=el('button','a-btn primary','Save');sv.type='button';sv.onclick=function(){sv.disabled=true;api('POST','/indexers',val()).then(function(){md.close();after()}).catch(function(e){sv.disabled=false;setMsg(msg,e.message,true)})};
    md.foot.appendChild(tb);md.foot.appendChild(cn);md.foot.appendChild(sv)}
  function idxImport(after){var md=aModal('Import from Prowlarr','760px');var f=aForm();md.body.appendChild(f.el);
    var u=el('input','a-input');u.id='pu';u.placeholder='http://10.0.0.8:9696';f.row('Prowlarr address',u,null,'pu');var k=el('input','a-input');k.id='pk';k.type='password';f.row('API key',k,'Prowlarr → Settings → General.','pk');
    md.body.appendChild(el('div','a-dim a-small','Usenet and Torznab indexers are added directly when Prowlarr shows their address and key. The others (Prowlarr\'s own tracker definitions) are reached through Prowlarr for now and marked "via prowlarr".'));
    var msg=aMsg();md.body.appendChild(msg);var cn=el('button','a-btn','Close');cn.type='button';cn.onclick=function(){md.close();after()};var ok=el('button','a-btn primary','Import');ok.type='button';
    ok.onclick=function(){ok.disabled=true;setMsg(msg,'importing…');api('POST','/indexers/import-prowlarr',{url:u.value,key:k.value}).then(function(r){setMsg(msg,r.added.length?'Added: '+r.added.join(', '):'Nothing new to add.')}).catch(function(e){ok.disabled=false;setMsg(msg,e.message,true)})};md.foot.appendChild(cn);md.foot.appendChild(ok)}
  function libraryView(body){
    body.appendChild(el('h2','ptitle','Library'));var host=el('div');host.style.marginTop='18px';body.appendChild(host);
    api('GET','/library').then(function(d){
      var errs=d.errors||[];if(errs.length){host.appendChild(el('h3','osec','Problems · '+errs.length));var c=el('div','ncard');errs.slice(0,10).forEach(function(e){c.appendChild(el('div','reason',e))});host.appendChild(c)}
      var seasons=d.seasons||[],shows={};seasons.forEach(function(x){(shows[x.showId]=shows[x.showId]||{name:x.Show||x.show,list:[]}).list.push(x)});
      var ids=Object.keys(shows).sort(function(a,b){return shows[a].name.localeCompare(shows[b].name)});
      host.appendChild(el('h3','osec','Series · '+ids.length));
      if(!ids.length)host.appendChild(el('div','empty','No series are being tracked.'));
      var box=el('div','jlist');
      ids.forEach(function(id){var sh=shows[id],imp=0,tot=0;sh.list.sort(function(a,b){return a.season-b.season});sh.list.forEach(function(x){imp+=x.imported;tot+=x.total});
        var row=el('div','jrow lrow');var n=el('div','jname');n.appendChild(el('div','',sh.name));n.appendChild(el('div','reason',imp+' / '+tot+' episodes imported'));row.appendChild(n);
        var cells=el('div','lcells');sh.list.forEach(function(x){var pct=x.total?100*x.imported/x.total:0;var c=el('span','lc '+(x.total&&x.imported>=x.total?'done':x.imported?'part':'none'));c.title='Season '+x.season+': '+x.imported+' / '+x.total;var f=el('i');f.style.height=pct+'%';c.appendChild(f);c.appendChild(el('b','',String(x.season)));cells.appendChild(c)});row.appendChild(cells);box.appendChild(row)});
      host.appendChild(box);
      var miss=d.missing||[];host.appendChild(el('h3','osec','Missing · '+miss.length));
      if(!miss.length)host.appendChild(el('div','empty','Nothing is missing.'));else{var mb=el('div','jlist');miss.slice(0,60).forEach(function(m){var r=el('div','jrow lrow');var n=el('div','jname');n.appendChild(el('div','',m.title));n.appendChild(el('div','reason',[m.kind,m.detail,m.category].filter(Boolean).join(' · ')));r.appendChild(n);mb.appendChild(r)});host.appendChild(mb)}
    }).catch(fail)}
  function overviewView(body){
    var head=el('div','ohead');head.appendChild(el('h2','ptitle','Overview'));var upd=el('span','reason','');head.appendChild(upd);body.appendChild(head);
    var chips=el('div','chips');body.appendChild(chips);
    var cols=el('div','ocols');var left=el('div'),right=el('div');cols.appendChild(left);cols.appendChild(right);body.appendChild(cols);
    var reqHost=el('div');var sysInfo=null,reqList=null;
    function drawChips(){chips.replaceChildren();if(!sysInfo)return;
      function chip2(t,st){chips.appendChild(el('span','chip2 '+(st||'ok'),t))}
      chip2(sysInfo.paused?'Intake paused':'Intake running',sysInfo.paused?'warn':'ok');
      if(sysInfo.torbox.configured)chip2(sysInfo.torbox.backoffUntil?'TorBox backing off':'TorBox '+sysInfo.torbox.used+' of '+sysInfo.torbox.limit+' this hour',sysInfo.torbox.backoffUntil?'warn':'ok');
      chip2(sysInfo.tdarr.api?'Tdarr '+sysInfo.tdarr.inFlight+' of '+sysInfo.tdarr.max+' busy':'Tdarr: folders only',sysInfo.tdarr.api?'ok':'dim');
      if(sysInfo.complete&&sysInfo.complete.totalBytes){var fr=sysInfo.complete.freeBytes/sysInfo.complete.totalBytes;chip2('Complete folder '+Math.round(fr*100)+'% free',fr<.1?'bad':'ok')}}
    function drawReqs(){reqHost.replaceChildren();if(reqList===null)return;setReqBadge(reqList.length);var h=el('h3','osec','Requests · '+reqList.length+' waiting');h.style.marginTop='22px';reqHost.appendChild(h);
      if(!reqList.length){reqHost.appendChild(el('div','empty','Nothing is waiting.'));return}
      var box=el('div','okv');reqList.slice(0,5).forEach(function(r){box.appendChild(reqRow(r,pollReqs,true))});reqHost.appendChild(box)}
    function pollReqs(){loadReqs().then(function(l){reqList=l;drawReqs()})}
    function draw(){
      drawChips();drawReqs();
      upd.textContent='Updated '+new Date().toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'});
      var need=lastRows.filter(function(r){return r.attention&&r.state!=='cleaned'});
      left.replaceChildren();left.appendChild(el('h3','osec','Needs you · '+need.length));
      if(!need.length)left.appendChild(el('div','empty','Nothing needs you right now.'));
      need.forEach(function(j){
        var c=el('div','ncard');var h=el('div','nh');var a=el('button','link',j.name);a.onclick=function(){tab='jobs';detailId=j.id;app()};h.appendChild(a);h.appendChild(el('span','fpill failed',j.state==='failed'?'Failed':'Attention'));c.appendChild(h);
        c.appendChild(el('div','reason',(j.flag?j.flag+' ':'')+(j.reason||'')));c.appendChild(jobActions(j));left.appendChild(c)});
      right.replaceChildren();right.appendChild(el('h3','osec','Pipeline now'));
      var cnt=function(f){return lastRows.filter(f).length};
      var box=el('div','okv');
      [['Downloading',cnt(function(r){return r.state==='downloading'||r.state==='pulling'})],['Waiting for a converter',cnt(function(r){return r.state==='queued_transcode'})],['Converting',cnt(function(r){return r.state==='transcoding'||r.state==='verifying'})],['Waiting on the arr',cnt(function(r){return r.state==='ready'})],['Finished',cnt(function(r){return r.group==='done'})]].forEach(function(x){var r=el('div','orow');r.appendChild(el('span','',x[0]));r.appendChild(el('b','',String(x[1])));box.appendChild(r)});
      right.appendChild(box);
      right.appendChild(reqHost);
      var ac=attentionCount(lastRows);if(tabBtns.jobs){tabBtns.jobs.textContent='Jobs';if(ac){var bd=el('span','badge',String(ac));tabBtns.jobs.appendChild(bd)}}
    }
    function poll(){api('GET','/jobs').then(function(d){lastRows=Array.isArray(d)?d:(d.jobs||[]);draw()}).catch(fail)}
    function pollSys(){api('GET','/system').then(function(d){sysInfo=d;drawChips()}).catch(function(){})}
    poll();pollSys();pollReqs();timer=setInterval(function(){poll();pollSys();pollReqs()},5000);
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
    var conn=el('div','card');conn.appendChild(el('h4','','Connect an arr to Undermynd'));
    var host=location.hostname;
    conn.appendChild(help('In Sonarr, Radarr or Lidarr: Settings → Download Clients → add. Use SABnzbd for usenet and qBittorrent for torrents.'));
    [['host',host],['port','8081 (the LAN-only machine port)'],['SABnzbd API key','the client password (see the System tab)'],['qBittorrent login','username '+S.clientUser+' and the client password'],['category','one of the names below, exactly']].forEach(function(p){var d=el('div','kv');d.appendChild(el('span','k',p[0]));d.appendChild(el('span','v',p[1]));conn.appendChild(d)});
    body.appendChild(conn);
    var cats=el('div');body.appendChild(cats);
    function add(c){
      var card=el('div','card cat kind-'+(c.kind||'none'));var f={};var g=el('div','grid3');f.orig=c.name||'';
      f.name=row(g,'category name',{value:c.name,help:'what the arr sends, e.g. tv'});
      var w=el('div','fld');w.appendChild(el('label','','arr type'));var k=el('select');[['sonarr','sonarr'],['radarr','radarr'],['lidarr','lidarr'],['rss','no arr (RSS / watch folder)']].forEach(function(p){var x=p[0];var o=el('option','',p[1]);o.value=x;if(c.kind===x)o.selected=true;k.appendChild(o)});w.appendChild(k);g.appendChild(w);f.kind=k;
      f.arrUrl=row(g,'arr address',{value:c.arrUrl,ph:'http://10.0.0.5:8989',help:'so Undermynd can confirm the import'});
      f.arrApiKey=row(g,'arr API key',{type:'password',ph:c.hasArrApiKey?'set — leave blank to keep':'',help:'in the arr: Settings → General'});
      f.transcode=row(g,'transcode with Tdarr',{type:'checkbox',value:c.transcode,help:'off for music'});
      f.libraryDir=row(g,'library folder',{value:c.libraryDir,ph:'/media/Anime',help:'only for “no arr” categories: finished episodes are matched to a show folder here and renamed'});
      var pw=el('div','fld');pw.appendChild(el('label','','if the transcode fails'));var pf=el('select');[['fail','fail the grab (the arr blocklists and searches again)'],['passthrough','keep the original file']].forEach(function(x){var o=el('option','',x[1]);o.value=x[0];if((c.onTranscodeFail||'fail')===x[0])o.selected=true;pf.appendChild(o)});pw.appendChild(pf);g.appendChild(pw);f.onFail=pf;
      card.appendChild(g);var tt=el('button','','test arr connection');var tmsg=el('span','msg');tmsg.style.marginLeft='12px';
      tt.onclick=function(){tmsg.className='msg';tmsg.textContent='testing…';api('POST','/arrs/test',{name:f.name.value,origName:f.orig,kind:f.kind.value,url:f.arrUrl.value,apiKey:f.arrApiKey.value}).then(function(r){tmsg.className='msg ok';tmsg.textContent='connected: '+r.app+' '+r.version}).catch(function(e){tmsg.className='msg err';tmsg.textContent=e.message})};
      card.appendChild(tt);card.appendChild(tmsg);
      var sw=el('button','','place files already in this category\u2019s folder');sw.style.marginLeft='12px';sw.title='uses the saved library folder; save first if you just changed it';var smsg=el('span','msg');smsg.style.marginLeft='12px';
      sw.onclick=function(){smsg.className='msg';smsg.textContent='placing…';api('POST','/library/sweep',{name:f.orig||f.name.value}).then(function(r){smsg.className='msg ok';smsg.textContent=r.result}).catch(function(e){smsg.className='msg err';smsg.textContent=e.message})};
      if(c.libraryDir){card.appendChild(sw);card.appendChild(smsg)}card.appendChild(document.createElement('br'));
      var rm=el('button','bad','remove category');rm.onclick=function(){card.remove()};card.appendChild(rm);card._f=f;cats.appendChild(card)
    }
    S.categories.forEach(add);
    var a=el('button','','+ add category');a.onclick=function(){add({kind:'radarr',transcode:true})};body.appendChild(a);
    saveBar(body,function(m){save(function(b){b.categories=[].map.call(cats.children,function(c){var f=c._f;return {name:f.name.value,origName:f.orig,kind:f.kind.value,libraryDir:f.libraryDir.value,arrUrl:f.arrUrl.value,arrApiKey:f.arrApiKey.value,transcode:f.transcode.checked,onTranscodeFail:f.onFail.value}})},m)})
  }

  function clientsPane(body){
    body.appendChild(head('Download clients','Undermynd downloads through TorBox when a token is set. Otherwise usenet goes to NZBGet and torrents go to qBittorrent, both optional and local.'));
    var tb=el('div','card');var h=el('h4','','TorBox ');h.appendChild(chip(S.torbox.hasToken,S.torbox.hasToken?'token set':'no token'));tb.appendChild(h);
    var g=el('div','grid3');
    var tok=row(g,'API token',{type:'password',ph:S.torbox.hasToken?'set — leave blank to keep':'paste your token',help:'TorBox → Settings → API'});
    var cph=row(g,'new downloads per hour',{type:'number',value:S.torbox.createPerHour,help:'TorBox’s limit on creating downloads'});
    var pa=row(g,'files copied at once',{type:'number',value:S.torbox.pullersAtOnce,help:'how many finished files are pulled down in parallel'});
    var lg=row(g,'seconds between link requests',{type:'number',value:S.torbox.linkGapSeconds,help:'spaces out requests so TorBox does not throttle'});
    tb.appendChild(g);body.appendChild(tb);
    var ng=el('div','card');var h2=el('h4','','NZBGet (usenet fallback) ');h2.appendChild(chip(!!S.nzbget.url,S.nzbget.url?'configured':'not set'));ng.appendChild(h2);
    var g2=el('div','grid3');
    var nu=row(g2,'address',{value:S.nzbget.url,ph:'http://10.0.0.6:6789'});var nn=row(g2,'username',{value:S.nzbget.username});
    var np=row(g2,'password',{type:'password',ph:S.nzbget.hasPassword?'set — leave blank to keep':''});
    var nd=row(g2,'destination folder',{value:S.nzbget.destRoot,help:'NZBGet’s DestDir, at the same path in both containers'});
    ng.appendChild(g2);body.appendChild(ng);
    var qb=el('div','card');var h3=el('h4','','qBittorrent (torrent fallback) ');h3.appendChild(chip(!!S.qbit.url,S.qbit.url?'configured':'not set'));qb.appendChild(h3);
    var g3=el('div','grid3');
    var qu=row(g3,'address',{value:S.qbit.url,ph:'http://10.0.0.7:8080'});var qn=row(g3,'username',{value:S.qbit.username});
    var qp=row(g3,'password',{type:'password',ph:S.qbit.hasPassword?'set — leave blank to keep':''});
    var qd=row(g3,'save folder',{value:S.qbit.saveDir,help:'same path in both containers'});
    qb.appendChild(g3);body.appendChild(qb);
    saveBar(body,function(m){save(function(b){
      b.torbox={token:tok.value,createPerHour:+cph.value,pullersAtOnce:+pa.value,linkGapSeconds:+lg.value};
      b.nzbget={url:nu.value,username:nn.value,password:np.value,destRoot:nd.value};
      b.qbit={url:qu.value,username:qn.value,password:qp.value,saveDir:qd.value}},m).then(function(){pane(body)})})
  }

  function transcodePane(body){
    body.appendChild(head('Transcode (Tdarr)','Videos for categories with transcode on are handed to a dedicated Tdarr library. Undermynd watches Tdarr over its API, so it can show progress and notice failures.'));
    var c0=el('div','card');var h0=el('h4','','Connection ');h0.appendChild(chip(!!S.tdarr.url,S.tdarr.url?'configured':'not set — folder hand-off only'));c0.appendChild(h0);
    var g0=el('div','grid3');
    var url=row(g0,'Tdarr address',{value:S.tdarr.url,ph:'http://10.0.0.8:8265',help:'the Tdarr server, port 8265 by default'});
    var key=row(g0,'API key',{type:'password',ph:S.tdarr.hasApiKey?'set — leave blank to keep':'only if Tdarr has authentication on',help:'Tdarr → Tools → API Keys'});
    var lib=row(g0,'library ID',{value:S.tdarr.libraryId,help:'the ID of the dedicated Undermynd library (shown in the library’s settings)'});
    c0.appendChild(g0);var tb=el('button','','test connection');var tm=el('span','msg');tm.style.marginLeft='12px';
    tb.onclick=function(){tm.className='msg';tm.textContent='testing…';api('POST','/tdarr/test',{url:url.value,apiKey:key.value}).then(function(r){tm.className='msg ok';tm.textContent='connected: '+r.nodes+' node(s), '+r.busyWorkers+' busy worker(s)'}).catch(function(e){tm.className='msg err';tm.textContent=e.message})};
    c0.appendChild(tb);c0.appendChild(tm);body.appendChild(c0);
    var c=el('div','card');c.appendChild(el('h4','','Folders and limits'));var g=el('div','grid3');
    var inn=row(g,'input folder (library source)',{value:S.tdarr.inDir,help:'the same path inside Undermynd and Tdarr'});
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
    body.appendChild(head('Playback (Tracearr and Emby)','Feeds the public page: Now Playing and Recently Added come from Tracearr (music is added from Lidarr). Emby is only used to hide whole libraries from Now Playing.'));
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
    var c3=el('div','card');c3.appendChild(el('h4','','Awaiting approval'));var lst=el('div');c3.appendChild(lst);body.appendChild(c3);
    function loadPending(){lst.replaceChildren();api('GET','/requests').then(function(r){if(!r.requests.length){lst.appendChild(help('Nothing is waiting.'));return}
      r.requests.forEach(function(q){var d=el('div','kv');d.appendChild(el('span','k',(q.title||'Untitled')+' ('+(q.media_type==='tv'?'TV show':'Movie')+')'));d.appendChild(el('span','v',q.requested_by||''));
        [['approve','approve'],['decline','deny']].forEach(function(a){var b=el('button','',a[1]);b.onclick=function(){b.disabled=true;api('POST','/requests/'+q.source_id+'/'+a[0]).then(loadPending).catch(function(e){b.disabled=false;alert(e.message)})};d.appendChild(b)});lst.appendChild(d)})}).catch(function(){lst.appendChild(help('Seerr is not reachable or not set up yet.'))})}
    loadPending();
    accessCard(body);
    saveBar(body,function(m){save(function(b){b.requests={enabled:re.checked,url:ru.value,apiKey:rk.value,userId:+ri.value};b.donations={title:ti.value,paypal_url:pp.value,cashapp_url:ca.value,custom_label:cl.value,custom_url:cu.value}},m).then(function(){pane(body)})})
  }

  function accessCard(body){
    var c=el('div','card');var h=el('h4','','Account requests ("need access?") ');var hc=el('span');h.appendChild(hc);c.appendChild(h);
    c.appendChild(help('Visitors ask for a media server account. Approving creates a disabled copy of your template user, and the visitor finishes setup from an emailed single-use link. The media server address and key come from the Playback tab. Emails are sent over TLS/STARTTLS only.'));
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
    var names={pending:'awaiting approval',incomplete:'setup needs attention',provisioning:'creating account',awaiting_setup:'waiting for the visitor to finish setup',setting_up:'finishing setup',active:'active',declined:'declined'};
    function act(id,a,b,note){b.disabled=true;api('POST','/access/requests/'+id+'/'+a,note||{}).then(loadQ).catch(function(e){b.disabled=false;alert(e.message)})}
    function loadQ(){api('GET','/access/requests').then(function(r){lst.replaceChildren();var items=r.requests||[];if(!items.length){lst.appendChild(help('No account requests yet.'));return}
      items.forEach(function(it){var d=el('div','kv');d.appendChild(el('span','k',it.name+' — '+(names[it.status]||it.status)));d.appendChild(el('span','v',it.email+' · '+it.username+(it.connect_username?' · Connect email '+it.connect_username:'')));
        [it.error,it.mail_error].forEach(function(t){if(t){var e=el('div','msg err',t);d.appendChild(e)}});
        var acts=[];
        if(it.status==='pending')acts=[['approve','approve'],['decline','decline'],['resend','resend notices']];
        else if(it.status==='incomplete')acts=[['approve','retry setup']];
        else if(it.status==='awaiting_setup')acts=[['resend','send fresh setup link']];
        else if(it.status==='active')acts=[['resend','resend sign-in instructions']];
        else if(it.status==='declined')acts=[['resend','resend notice']];
        if(it.emby_id&&['incomplete','awaiting_setup','active'].indexOf(it.status)>=0)acts.push(['reapply-template','reapply template'],['reset','reset after server deletion']);
        acts.forEach(function(a){var b=el('button','',a[1]);b.onclick=function(){act(it.id,a[0],b)};d.appendChild(b)});
        if(it.status==='pending'||it.status==='incomplete'){var b2=el('button','','correct details');b2.onclick=function(){var em=prompt_('Notification email',it.email);if(em==null)return;var cn=it.connect_username?prompt_('Emby Connect email',it.connect_username):'';if(cn==null)return;b2.disabled=true;api('POST','/access/requests/'+it.id+'/correct',{email:em,connect_username:cn}).then(loadQ).catch(function(e){b2.disabled=false;alert(e.message)})};d.appendChild(b2)}
        lst.appendChild(d)})}).catch(function(e){lst.replaceChildren(help(e.message))})}
    function prompt_(l,v){return window.prompt(l,v)}
    api('GET','/access').then(fill).catch(function(e){msg.className='msg err';msg.textContent=e.message});loadQ();
  }

  function rssPane(body){
    body.appendChild(head('RSS auto-download','Feeds are checked on a schedule. The first check of a new feed only records what is already there, so nothing old gets downloaded. The first rule that matches an item starts it.'));
    body.appendChild(el('div','sec','what the feeds are reading'));
    var live=el('div');body.appendChild(live);
    var cats=[];api('GET','/settings').then(function(v){cats=(v.categories||[]).map(function(c){return c.name})}).catch(function(){});
    // "download" turns into a category picker; starting it goes through the same path as a rule.
    function manual(feed,idx,title,cell){var btn=el('button','','download');btn.onclick=function(){
      var sel=document.createElement('select');cats.forEach(function(n){var o=document.createElement('option');o.textContent=n;sel.appendChild(o)});
      var go=el('button','go','start'),no=el('button','','cancel'),msg=el('span','msg');
      function back(){cell.replaceChildren(manual(feed,idx,title,cell))}
      no.onclick=back;
      go.onclick=function(){go.disabled=true;msg.textContent='starting…';api('POST','/rss/download',{feed:feed,index:idx,title:title,category:sel.value}).then(function(){showFeeds()}).catch(function(e){go.disabled=false;msg.className='msg err';msg.textContent=e.message})};
      [sel,go,no,msg].forEach(function(x){x.style.marginRight='6px';cell.appendChild(x)});btn.remove()};return btn}
    function showFeeds(){api('GET','/rss/status').then(function(list){live.textContent='';if(!list.length)live.appendChild(help('No feeds yet.'));list.forEach(function(st){
      var card=el('div','card');var h=el('h4','',st.name+'  ');var when=st.checkedAt&&st.checkedAt.indexOf('0001')!==0?'checked '+new Date(st.checkedAt).toLocaleTimeString():'not checked yet';h.appendChild(el('span','',when));card.appendChild(h);
      if(st.error){var er=el('div','msg err',st.error);card.appendChild(er)}
      var t=document.createElement('table');(st.items||[]).forEach(function(it,idx){var tr=document.createElement('tr');var a=document.createElement('td');a.textContent=it.title;var b=document.createElement('td');b.textContent=it.outcome;b.style.opacity='.75';b.style.paddingLeft='16px';b.style.whiteSpace='nowrap';tr.appendChild(a);tr.appendChild(b);var c=document.createElement('td');c.style.whiteSpace='nowrap';c.style.paddingLeft='12px';if(it.outcome.indexOf('started')!==0)c.appendChild(manual(st.name,idx,it.title,c));tr.appendChild(c);t.appendChild(tr)});
      if(st.items&&st.items.length)card.appendChild(t);else if(!st.error)card.appendChild(help('Nothing read from this feed yet.'));
      live.appendChild(card)})}).catch(function(e){live.textContent=e.message})}
    var chk=el('button','','check feeds now');chk.onclick=function(){chk.textContent='checking…';api('POST','/rss/check',{}).then(function(){chk.textContent='check feeds now';showFeeds()}).catch(function(){chk.textContent='check feeds now'})};body.appendChild(chk);
    showFeeds();
    body.appendChild(el('div','sec','feeds'));
    var feeds=el('div');body.appendChild(feeds);
    function addFeed(c){var card=el('div','card');var g=el('div','grid3');var f={};
      f.name=row(g,'name',{value:c.name});f.url=row(g,'feed address',{value:c.url,help:'the full RSS link, including any key'});
      f.intervalMinutes=row(g,'check every (minutes)',{type:'number',value:c.intervalMinutes||15});f.enabled=row(g,'enabled',{type:'checkbox',value:c.enabled!==false});
      card.appendChild(g);var rm=el('button','bad','remove feed');rm.onclick=function(){card.remove()};card.appendChild(rm);card._f=f;feeds.appendChild(card)}
    S.feeds.forEach(addFeed);var af=el('button','','+ add feed');af.onclick=function(){addFeed({enabled:true,intervalMinutes:15})};body.appendChild(af);
    body.appendChild(el('div','sec','rules'));
    body.appendChild(help('Patterns are regular expressions and ignore case. Example: must contain  S\\d+E\\d+.*1080p   must not contain  cam|hdts'));
    var rules=el('div');body.appendChild(rules);
    function addRule(c){var card=el('div','card');var g=el('div','grid3');var f={};
      f.name=row(g,'rule name',{value:c.name});f.feed=row(g,'only this feed',{value:c.feed,help:'blank = every feed'});
      f.category=row(g,'category',{value:c.category,help:'one of your categories'});
      f.mustContain=row(g,'must contain',{value:c.mustContain});f.mustNotContain=row(g,'must not contain',{value:c.mustNotContain});
      f.enabled=row(g,'enabled',{type:'checkbox',value:c.enabled!==false});
      card.appendChild(g);var rm=el('button','bad','remove rule');rm.onclick=function(){card.remove()};card.appendChild(rm);card._f=f;rules.appendChild(card)}
    S.rules.forEach(addRule);var ar=el('button','','+ add rule');ar.onclick=function(){addRule({enabled:true})};body.appendChild(ar);
    body.appendChild(el('div','sec','watched folders'));
    body.appendChild(help('Drop a .torrent, .magnet or .nzb file in a folder and it goes through the same flow. Handled files move to a submitted-to-undermynd subfolder. Finished files are kept in the Complete folder under the category. Do not point this at a folder another app also watches.'));
    var watches=el('div');body.appendChild(watches);
    function addWatch(c){var card=el('div','card');var g=el('div','grid3');var f={};
      f.dir=row(g,'folder',{value:c.dir,help:'full path as the container sees it'});
      f.category=row(g,'category',{value:c.category,help:'blank = the category of your first enabled RSS rule'});
      f.enabled=row(g,'enabled',{type:'checkbox',value:c.enabled!==false});
      card.appendChild(g);var rm=el('button','bad','remove folder');rm.onclick=function(){card.remove()};card.appendChild(rm);card._f=f;watches.appendChild(card)}
    (S.watches||[]).forEach(addWatch);var aw=el('button','','+ add folder');aw.onclick=function(){addWatch({enabled:true})};body.appendChild(aw);
    saveBar(body,function(m){save(function(b){
      b.watches=[].map.call(watches.children,function(c){var f=c._f;return {dir:f.dir.value,category:f.category.value,enabled:f.enabled.checked}});
      b.feeds=[].map.call(feeds.children,function(c){var f=c._f;return {name:f.name.value,url:f.url.value,intervalMinutes:+f.intervalMinutes.value,enabled:f.enabled.checked}});
      b.rules=[].map.call(rules.children,function(c){var f=c._f;return {name:f.name.value,feed:f.feed.value,mustContain:f.mustContain.value,mustNotContain:f.mustNotContain.value,category:f.category.value,enabled:f.enabled.checked}})},m)})
  }

  function systemPane(body){
    body.appendChild(head('System','What the pipeline is doing right now, the login the arrs use, and timing.'));
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
        var r1=el('div','kv');r1.appendChild(el('span','k','new downloads this hour'));r1.appendChild(el('span','v',d.torbox.used+' of '+d.torbox.limit));c2.appendChild(r1);
        var br=el('div','bar');var bf=el('i');bf.style.width=Math.min(100,d.torbox.limit?d.torbox.used*100/d.torbox.limit:0)+'%';br.appendChild(bf);c2.appendChild(br);
        if(d.torbox.backoffUntil){var r2=el('div','kv');r2.appendChild(el('span','k','backing off until'));r2.appendChild(el('span','v flag',new Date(d.torbox.backoffUntil).toLocaleTimeString()));c2.appendChild(r2)}}
      g.appendChild(c2);
      var c3=el('div','card');c3.appendChild(el('h4','','Tdarr'));var t1=el('div','kv');t1.appendChild(el('span','k','jobs with Tdarr'));t1.appendChild(el('span','v',d.tdarr.inFlight+' of '+d.tdarr.max));c3.appendChild(t1);
      var t2=el('div','kv');t2.appendChild(el('span','k','watched over the API'));t2.appendChild(chip(d.tdarr.api,d.tdarr.api?'yes':'no — folders only'));c3.appendChild(t2);g.appendChild(c3);
      [['Work folder',d.work],['Complete folder',d.complete]].forEach(function(p){var c=el('div','card');c.appendChild(el('h4','',p[0]));var a=el('div','kv');a.appendChild(el('span','k','path'));a.appendChild(el('span','v',p[1].path));c.appendChild(a);
        var b=el('div','kv');b.appendChild(el('span','k','used by Undermynd'));b.appendChild(el('span','v',bytes(p[1].usedByUs)+(p[1].partial?' (partial count)':'')));c.appendChild(b);
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
