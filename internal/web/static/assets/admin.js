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
    clearInterval(timer);logoutBtn.hidden=true;
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
  var tab='jobs',detailId=null;
  function app(){
    logoutBtn.hidden=false;logoutBtn.onclick=function(){api('POST','/logout').then(start)};
    root.replaceChildren();
    var tabs=el('div','tabs');['jobs','settings'].forEach(function(t){var b=el('button',t===tab?'on':'',t);b.onclick=function(){tab=t;detailId=null;app()};tabs.appendChild(b)});
    root.appendChild(tabs);var body=el('div');root.appendChild(body);
    if(tab==='jobs')jobsView(body);else settingsView(body);
  }
  function fail(e){if(e.status===401){start()}else{var d=el('div','err',e.message);root.appendChild(d)}}
  function ago(t){var s=Math.max(0,(Date.now()-new Date(t))/1000);if(s<90)return Math.round(s)+'s';if(s<5400)return Math.round(s/60)+'m';if(s<172800)return Math.round(s/3600)+'h';return Math.round(s/86400)+'d'}
  function jobsView(body){
    function draw(){
      api('GET','/jobs').then(function(list){
        if(tab!=='jobs'||detailId)return;
        body.replaceChildren();
        if(!list.length){body.appendChild(el('div','empty','no jobs yet'));return}
        var t=el('table');var h=el('tr');['name','source','state','%','backend','age',''].forEach(function(x){h.appendChild(el('th','',x))});t.appendChild(h);
        list.forEach(function(j){
          var r=el('tr');var n=el('td');var a=el('button','link',j.name);a.onclick=function(){detailId=j.id;detail(body,j.id)};n.appendChild(a);
          if(j.flag)n.appendChild(el('div','flag',j.flag));if(j.reason&&j.state!=='cleaned')n.appendChild(el('div','reason',j.reason));r.appendChild(n);
          r.appendChild(el('td','mono',j.source+' / '+j.category));
          var st=el('td','',j.state);if(j.state==='failed')st.style.color='var(--red)';r.appendChild(st);
          r.appendChild(el('td','',Math.round(j.progress*100)+'%'));r.appendChild(el('td','mono',j.backend||'-'));r.appendChild(el('td','mono',ago(j.created)));
          var act=el('td');
          if(j.state==='failed'){var rt=el('button','go','retry');rt.onclick=function(){doAction(j.id,'retry')};act.appendChild(rt)}
          if(j.state==='ready'){var mi=el('button','','mark imported');mi.onclick=function(){doAction(j.id,'imported')};act.appendChild(mi)}
          if(j.state!=='cleaned'){var cc=el('button','bad','cancel');cc.onclick=function(){if(confirm('Cancel and clean up this job?'))doAction(j.id,'cancel')};act.appendChild(document.createTextNode(' '));act.appendChild(cc)}
          r.appendChild(act);t.appendChild(r);
        });
        body.appendChild(t);
      }).catch(fail);
    }
    draw();timer=setInterval(draw,3000);
  }
  function doAction(id,a){api('POST','/jobs/'+encodeURIComponent(id)+'/'+a).then(function(){app()}).catch(fail)}
  function detail(body,id){
    clearInterval(timer);body.replaceChildren();
    var back=el('button','link','← all jobs');back.onclick=function(){detailId=null;app()};body.appendChild(back);
    api('GET','/jobs/'+encodeURIComponent(id)).then(function(j){
      body.appendChild(el('h3','',j.name));
      var c=el('div','card');[['state',j.state],['reason',j.reason||'-'],['id (download id)',j.id],['category',j.category],['source',j.source],['backend',j.backend||'-'],['complete folder',j.completeDir||'-'],['files',(j.files||[]).join(', ')||'-']].forEach(function(p){var d=el('div');d.appendChild(el('span','prompt',p[0]+': '));d.appendChild(document.createTextNode(p[1]));c.appendChild(d)});
      body.appendChild(c);body.appendChild(el('div','sec','── history '));
      var t=el('table');(j.history||[]).slice().reverse().forEach(function(h){var r=el('tr');r.appendChild(el('td','mono',new Date(h.at).toLocaleString()));r.appendChild(el('td','',(h.from||'·')+' → '+h.to));r.appendChild(el('td','reason',h.reason||''));t.appendChild(r)});body.appendChild(t);
    }).catch(fail);
  }
  function settingsView(body){
    api('GET','/settings').then(function(s){
      body.appendChild(el('div','sec','── download client login for the arrs '));
      body.appendChild(el('p','hint','In Sonarr, Radarr and Lidarr add a qBittorrent client: host = this machine, port = the LAN-only port (8081 by default), and this login. The password is never shown; generate a new one to see it once.'));
      var cu=el('div');cu.appendChild(el('span','prompt','username: '));cu.appendChild(document.createTextNode(s.clientUser));body.appendChild(cu);
      var out=el('pre');var gen=el('button','','generate new password');gen.onclick=function(){if(!confirm('This replaces the current password. The arrs must be updated.'))return;api('POST','/settings/client-password').then(function(r){out.textContent='username: '+r.user+'\npassword: '+r.password+'\n(shown once)'}).catch(fail)};body.appendChild(gen);body.appendChild(out);

      body.appendChild(el('div','sec','── categories '));
      body.appendChild(el('p','hint','The category an arr sends decides which arr confirms the import. Leave the API key blank to keep the stored one.'));
      var cats=el('div','cats');body.appendChild(cats);
      function addCat(c){var card=el('div','card');card._c=c;var f={};
        [['name','text','name'],['arrUrl','text','arr url (http://radarr:7878)'],['arrApiKey','password',c.hasArrApiKey?'api key (set — leave blank to keep)':'api key']].forEach(function(d){var w=el('div');w.appendChild(el('label','',d[2]));var i=el('input');i.type=d[1];i.value=d[0]==='arrApiKey'?'':(c[d[0]]||'');i.autocomplete='off';w.appendChild(i);card.appendChild(w);f[d[0]]=i});
        var w=el('div');w.appendChild(el('label','','kind'));var k=el('select');['radarr','sonarr','lidarr'].forEach(function(x){var o=el('option','',x);o.value=x;if(c.kind===x)o.selected=true;k.appendChild(o)});w.appendChild(k);f.kind=k;card.appendChild(w);
        var w2=el('div');w2.appendChild(el('label','','transcode with Tdarr'));var t=el('input');t.type='checkbox';t.checked=!!c.transcode;t.style.width='auto';w2.appendChild(t);f.transcode=t;card.appendChild(w2);
        var rm=el('button','bad','remove');rm.onclick=function(){card.remove()};card.appendChild(rm);card._f=f;cats.appendChild(card)}
      (s.categories||[]).forEach(addCat);
      var add=el('button','','add category');add.onclick=function(){addCat({name:'',kind:'radarr'})};body.appendChild(add);

      body.appendChild(el('div','sec','── local qBittorrent (used when TorBox is not configured) '));
      var q=el('div','grid');var qf={};
      [['url','text','url (http://qbittorrent:8080)'],['username','text','username'],['password','password',s.qbit.hasPassword?'password (set — leave blank to keep)':'password'],['saveDir','text','save folder (same path in both containers)']].forEach(function(d){var w=el('div');w.appendChild(el('label','',d[2]));var i=el('input');i.type=d[1];i.value=d[0]==='password'?'':(s.qbit[d[0]]||'');i.autocomplete='off';w.appendChild(i);q.appendChild(w);qf[d[0]]=i});body.appendChild(q);

      body.appendChild(el('div','sec','── timing '));
      var tm=el('div','grid');var tf={};[['stallMinutes','no-progress timeout (minutes)'],['reconcileSeconds','import check interval (seconds)'],['flagImportHours','flag a ready job after (hours)']].forEach(function(d){var w=el('div');w.appendChild(el('label','',d[1]));var i=el('input');i.type='number';i.value=s[d[0]];w.appendChild(i);tm.appendChild(w);tf[d[0]]=i});body.appendChild(tm);

      var save=el('button','go','save settings');var msg=el('div','err');
      save.onclick=function(){msg.className='err';msg.textContent='';
        var body2={categories:[].map.call(cats.children,function(card){var f=card._f;return {name:f.name.value,kind:f.kind.value,arrUrl:f.arrUrl.value,arrApiKey:f.arrApiKey.value,transcode:f.transcode.checked}}),
          qbit:{url:qf.url.value,username:qf.username.value,password:qf.password.value,saveDir:qf.saveDir.value},
          stallMinutes:+tf.stallMinutes.value,reconcileSeconds:+tf.reconcileSeconds.value,flagImportHours:+tf.flagImportHours.value};
        api('PUT','/settings',body2).then(function(){msg.className='ok';msg.textContent='saved'}).catch(function(e){msg.textContent=e.message})};
      body.appendChild(el('div'));body.appendChild(save);body.appendChild(msg);
    }).catch(fail);
  }
  start();
})();
