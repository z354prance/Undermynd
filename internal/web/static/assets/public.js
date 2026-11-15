(function(){
  function el(t,c,x){var e=document.createElement(t);if(c)e.className=c;if(x!=null)e.textContent=x;return e}
  function mb(n){if(!n)return '';var u=['B','KB','MB','GB','TB'],i=0;while(n>=1024&&i<4){n/=1024;i++}return n.toFixed(i>1?1:0)+' '+u[i]}
  function when(t){var d=new Date(t);return isNaN(d)?'':d.toLocaleString([], {month:'short',day:'numeric',hour:'numeric',minute:'2-digit'})}
  var list=document.getElementById('list'),seasons=document.getElementById('seasons'),recent=document.getElementById('recent');
  var order={downloading:0,processing:1,finishing:2,queued:3,failed:4};
  var open={};
  function sub(j){
    if(j.group==='processing'||j.stage==='Waiting for Tdarr')return ['Transcode','Tdarr'];
    if(j.group==='downloading'&&j.size)return [mb(j.done)+' of '+mb(j.size),j.via||''];
    return [j.stage,j.via||''];
  }
  function renderJobs(jobs){
    document.getElementById('jobCount').textContent=jobs.length;
    list.replaceChildren();
    if(!jobs.length){list.appendChild(el('div','empty','nothing in progress'));return}
    jobs.sort(function(a,b){return (order[a.group]-order[b.group])||a.title.localeCompare(b.title)});
    jobs.forEach(function(j){
      var r=el('div','job '+j.group),g=el('div','jgrid');
      var c1=el('div');c1.appendChild(el('div','nm',j.title));c1.appendChild(el('div','st',j.stage));g.appendChild(c1);
      var s=sub(j),c2=el('div');c2.appendChild(el('div','sub',s[0]));c2.appendChild(el('div','sub',s[1]));g.appendChild(c2);
      var c3=el('div'),b=el('div','bar'),i=el('i');i.style.width=Math.round(j.progress*100)+'%';b.appendChild(i);c3.appendChild(b);c3.appendChild(el('div','pct',Math.round(j.progress*100)+'%'));g.appendChild(c3);
      r.appendChild(g);
      var d=document.createElement('details');d.open=!!open[j.title];d.addEventListener('toggle',function(){open[j.title]=d.open});
      d.appendChild(el('summary','','Details'));var st=el('div','steps');
      (j.steps||[]).forEach(function(x){var row=el('div');row.appendChild(el('span','',x.stage));row.appendChild(el('span','',when(x.at)));st.appendChild(row)});
      d.appendChild(st);r.appendChild(d);list.appendChild(r)});
  }
  function renderLib(d){
    document.getElementById('seasonCount').textContent=d.seasons.length;
    document.getElementById('recentCount').textContent=d.recent.length;
    seasons.replaceChildren();recent.replaceChildren();
    if(!d.seasons.length)seasons.appendChild(el('div','empty','no shows in progress'));
    d.seasons.forEach(function(s){
      var r=el('div','srow');r.appendChild(el('div','n',s.show+' — season '+s.season));r.appendChild(el('div','tag',s.imported+'/'+s.total));
      var b=el('div','bar'),i=el('i');i.style.width=Math.round(100*s.imported/Math.max(s.total,1))+'%';b.appendChild(i);r.appendChild(b);seasons.appendChild(r)});
    if(!d.recent.length)recent.appendChild(el('div','empty','nothing imported yet'));
    var unit={episode:'episode',movie:'movie',album:'album'};
    d.recent.forEach(function(x){
      var it=el('div','item'),t=el('div','t');t.appendChild(el('b','',x.title));
      t.appendChild(el('small','','▸ '+x.count+' recently added '+(unit[x.kind]||'item')+(x.count>1?'s':'')));it.appendChild(t);
      it.appendChild(el('time','',new Date(x.date).toLocaleDateString()));recent.appendChild(it)});
  }
  function load(){fetch('/api/public/jobs').then(function(r){return r.json()}).then(renderJobs).catch(function(){})}
  function loadLib(){fetch('/api/public/library').then(function(r){return r.json()}).then(renderLib).catch(function(){})}
  load();setInterval(load,4000);loadLib();setInterval(loadLib,60000);
})();
