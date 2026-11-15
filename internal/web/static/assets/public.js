(function(){
  var list=document.getElementById('list');
  var order={downloading:0,processing:1,finishing:2,queued:3,failed:4};
  var title={downloading:'downloading',processing:'processing',finishing:'finishing up',queued:'queued',failed:'needs attention'};
  function el(t,c,x){var e=document.createElement(t);if(c)e.className=c;if(x!=null)e.textContent=x;return e}
  function render(jobs){
    list.replaceChildren();
    if(!jobs.length){list.appendChild(el('div','empty','nothing in progress'));return}
    jobs.sort(function(a,b){return (order[a.group]-order[b.group])||a.title.localeCompare(b.title)});
    var last='';
    jobs.forEach(function(j){
      if(j.group!==last){last=j.group;list.appendChild(el('div','sec','── '+(title[j.group]||j.group)+' '))}
      var r=el('div','row');
      r.appendChild(el('div','name',j.title));
      r.appendChild(el('div','tag '+j.group,Math.round(j.progress*100)+'%'));
      var b=el('div','bar'),i=el('i');i.style.width=Math.round(j.progress*100)+'%';b.appendChild(i);r.appendChild(b);
      list.appendChild(r);
    });
  }
  function load(){fetch('/api/public/jobs').then(function(r){return r.json()}).then(render).catch(function(){})}
  var seasons=document.getElementById('seasons');
  function renderLib(d){
    seasons.replaceChildren();
    if(!d.seasons.length)seasons.appendChild(el('div','empty','no shows in progress'));
    d.seasons.forEach(function(s){
      var r=el('div','row');r.appendChild(el('div','name',s.show+' — season '+s.season));
      r.appendChild(el('div','tag',s.imported+'/'+s.total));
      var b=el('div','bar'),i=el('i');i.style.width=Math.round(100*s.imported/Math.max(s.total,1))+'%';b.appendChild(i);r.appendChild(b);seasons.appendChild(r)});
  }
  function loadLib(){fetch('/api/public/library').then(function(r){return r.json()}).then(renderLib).catch(function(){})}
  load();setInterval(load,4000);loadLib();setInterval(loadLib,60000);
})();
