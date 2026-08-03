(() => {
  const $ = (id) => document.getElementById(id);
  const state = { user: null, movies: [], cinemas: [], auditoriums: [], tiers: [], shows: [], blocks: [], promotions: [] };
  const esc = (value) => String(value ?? "").replace(/[&<>'"]/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[char]);
  const money = (paise, currency = "INR") => new Intl.NumberFormat("en-IN", { style: "currency", currency }).format(Number(paise || 0) / 100);
  const localInput = (value) => value ? new Date(value).toLocaleString("sv-SE").slice(0, 16) : "";
  const iso = (value) => value ? new Date(value).toISOString() : null;
  const nullableNumber = (value) => value === "" ? null : Number(value);
  const hasRole = (role) => state.user?.roles?.includes(role);

  async function api(path, options = {}) {
    options.headers = { "Content-Type": "application/json", ...(options.headers || {}) };
    const response = await fetch(path, options);
    if (response.status === 204) return null;
    const data = await response.json().catch(() => ({ error: "Invalid server response" }));
    if (!response.ok) throw new Error(data.error || `Request failed (${response.status})`);
    return data;
  }
  function message(text, type = "success") {
    $("message").textContent = text;
    $("message").className = `message ${type}`;
    if (text) setTimeout(() => { if ($("message").textContent === text) $("message").textContent = ""; }, 6000);
  }
  async function perform(action, success) {
    try { await action(); message(success); } catch (error) { message(error.message, "error"); }
  }
  function statusBadge(active) { return `<span class="badge ${active ? "active" : "inactive"}">${active ? "Active" : "Inactive"}</span>`; }
  function optionList(items, label, includeBlank = false, blankLabel = "Select") {
    return `${includeBlank ? `<option value="">${esc(blankLabel)}</option>` : ""}${items.map((item) => `<option value="${item.id}">${esc(label(item))}</option>`).join("")}`;
  }
  function cinemaName(id) { return state.cinemas.find((item) => item.id === id)?.name || id; }
  function auditoriumName(id) { return state.auditoriums.find((item) => item.id === id)?.name || id; }
  function movieName(id) { return state.movies.find((item) => item.id === id)?.title || id; }

  document.querySelectorAll("nav button[data-panel]").forEach((button) => button.onclick = () => {
    document.querySelectorAll("nav button").forEach((item) => item.classList.toggle("active", item === button));
    document.querySelectorAll(".panel").forEach((panel) => panel.classList.toggle("active", panel.id === button.dataset.panel));
    $("pageTitle").textContent = button.textContent;
  });

  function resetMovie() { $("movieForm").reset(); $("movieID").value = ""; $("movieActive").checked = true; }
  function renderMovies() {
    $("movieCount").textContent = `${state.movies.length} total`;
    $("movieRows").innerHTML = state.movies.map((v) => `<tr><td><strong>${esc(v.title)}</strong><br><span class="muted">${esc(v.slug)}</span></td><td>${esc(v.language)} · ${esc(v.certificate)}</td><td>${v.duration_minutes} min</td><td>${statusBadge(v.active)}</td><td class="actions"><button data-edit-movie="${v.id}">Edit</button><button class="danger" data-delete-movie="${v.id}">Archive</button></td></tr>`).join("") || `<tr><td colspan="5" class="empty">No movies</td></tr>`;
  }
  async function loadMovies() { state.movies = await api("/api/v1/admin/movies"); renderMovies(); refreshSelects(); }
  $("movieForm").onsubmit = (event) => { event.preventDefault(); perform(async () => {
    const id = $("movieID").value;
    const body = { slug: $("movieSlug").value.trim(), title: $("movieTitle").value.trim(), duration_minutes: Number($("movieDuration").value), language: $("movieLanguage").value.trim(), certificate: $("movieCertificate").value.trim(), active: $("movieActive").checked };
    await api(id ? `/api/v1/admin/movies/${id}` : "/api/v1/admin/movies", { method: id ? "PUT" : "POST", body: JSON.stringify(body) }); resetMovie(); await loadMovies();
  }, "Movie saved"); };
  $("movieReset").onclick = resetMovie;
  $("movieRows").onclick = (event) => {
    const edit = event.target.dataset.editMovie, remove = event.target.dataset.deleteMovie;
    if (edit) { const v = state.movies.find((item) => item.id === edit); $("movieID").value=v.id; $("movieSlug").value=v.slug; $("movieTitle").value=v.title; $("movieDuration").value=v.duration_minutes; $("movieLanguage").value=v.language; $("movieCertificate").value=v.certificate; $("movieActive").checked=v.active; $("movieSlug").focus(); }
    if (remove && confirm("Archive this movie?")) perform(async () => { await api(`/api/v1/admin/movies/${remove}`, { method: "DELETE" }); await loadMovies(); }, "Movie archived");
  };

  function resetCinema() { $("cinemaForm").reset(); $("cinemaID").value=""; $("cinemaTimezone").value="Asia/Kolkata"; $("cinemaCutoff").value=120; $("cinemaRefund").value=100; $("cinemaActive").checked=true; }
  function renderCinemas() {
    $("cinemaCount").textContent = `${state.cinemas.length} total`;
    $("cinemaRows").innerHTML = state.cinemas.map((v) => `<tr><td><strong>${esc(v.name)}</strong><br><span class="muted">${esc(v.timezone)}</span></td><td>${esc(v.city)}, ${esc(v.region)}<br><span class="muted">${esc(v.address_line)}</span></td><td>${v.cancellation_refund_percent}% until ${v.cancellation_cutoff_minutes} min before</td><td>${statusBadge(v.active)}</td><td class="actions"><button data-edit-cinema="${v.id}">Edit</button><button class="danger" data-delete-cinema="${v.id}">Archive</button></td></tr>`).join("") || `<tr><td colspan="5" class="empty">No cinemas</td></tr>`;
  }
  async function loadCinemas() { state.cinemas = await api("/api/v1/admin/cinemas"); renderCinemas(); refreshSelects(); }
  $("cinemaForm").onsubmit = (event) => { event.preventDefault(); perform(async () => {
    const id=$("cinemaID").value; const body={name:$("cinemaName").value.trim(),timezone:$("cinemaTimezone").value.trim(),address_line:$("cinemaAddress").value.trim(),city:$("cinemaCity").value.trim(),region:$("cinemaRegion").value.trim(),postal_code:$("cinemaPostal").value.trim(),cancellation_cutoff_minutes:Number($("cinemaCutoff").value),cancellation_refund_percent:Number($("cinemaRefund").value),active:$("cinemaActive").checked};
    await api(id?`/api/v1/admin/cinemas/${id}`:"/api/v1/admin/cinemas",{method:id?"PUT":"POST",body:JSON.stringify(body)}); resetCinema(); await loadCinemas();
  }, "Cinema saved"); };
  $("cinemaReset").onclick=resetCinema;
  $("cinemaRows").onclick=(event)=>{ const edit=event.target.dataset.editCinema,remove=event.target.dataset.deleteCinema;if(edit){const v=state.cinemas.find((item)=>item.id===edit);$("cinemaID").value=v.id;$("cinemaName").value=v.name;$("cinemaTimezone").value=v.timezone;$("cinemaAddress").value=v.address_line;$("cinemaCity").value=v.city;$("cinemaRegion").value=v.region;$("cinemaPostal").value=v.postal_code;$("cinemaCutoff").value=v.cancellation_cutoff_minutes;$("cinemaRefund").value=v.cancellation_refund_percent;$("cinemaActive").checked=v.active;$("cinemaName").focus();}if(remove&&confirm("Archive this cinema?"))perform(async()=>{await api(`/api/v1/admin/cinemas/${remove}`,{method:"DELETE"});await loadCinemas();},"Cinema archived");};

  function resetAuditorium(){$("auditoriumForm").reset();$("auditoriumID").value="";$("auditoriumActive").checked=true;}
  function renderAuditoriums(){$("auditoriumCount").textContent=`${state.auditoriums.length} total`;$("auditoriumRows").innerHTML=state.auditoriums.map((v)=>`<tr><td><strong>${esc(v.name)}</strong></td><td>${esc(cinemaName(v.cinema_id))}</td><td>${esc(v.format)}</td><td>${statusBadge(v.active)}</td><td class="actions"><button data-layout-auditorium="${v.id}">Layout</button><button data-edit-auditorium="${v.id}">Edit</button><button class="danger" data-delete-auditorium="${v.id}">Archive</button></td></tr>`).join("")||`<tr><td colspan="5" class="empty">No auditoriums</td></tr>`;}
  async function loadAuditoriums(){state.auditoriums=await api("/api/v1/admin/auditoriums");renderAuditoriums();refreshSelects();}
  $("auditoriumForm").onsubmit=(event)=>{event.preventDefault();perform(async()=>{const id=$("auditoriumID").value;const body={cinema_id:$("auditoriumCinema").value,name:$("auditoriumName").value.trim(),format:$("auditoriumFormat").value.trim(),active:$("auditoriumActive").checked};await api(id?`/api/v1/admin/auditoriums/${id}`:"/api/v1/admin/auditoriums",{method:id?"PUT":"POST",body:JSON.stringify(body)});resetAuditorium();await loadAuditoriums();},"Auditorium saved");};
  $("auditoriumReset").onclick=resetAuditorium;
  $("auditoriumRows").onclick=(event)=>{const edit=event.target.dataset.editAuditorium,remove=event.target.dataset.deleteAuditorium,layout=event.target.dataset.layoutAuditorium;if(layout){$("layoutAuditorium").value=layout;loadLayout();document.querySelector('button[data-panel="venues"]').click();}if(edit){const v=state.auditoriums.find((item)=>item.id===edit);$("auditoriumID").value=v.id;$("auditoriumCinema").value=v.cinema_id;$("auditoriumName").value=v.name;$("auditoriumFormat").value=v.format;$("auditoriumActive").checked=v.active;$("auditoriumName").focus();}if(remove&&confirm("Archive this auditorium?"))perform(async()=>{await api(`/api/v1/admin/auditoriums/${remove}`,{method:"DELETE"});await loadAuditoriums();},"Auditorium archived");};
  async function loadLayout(){await perform(async()=>{$("layoutJSON").value=JSON.stringify(await api(`/api/v1/admin/auditoriums/${$("layoutAuditorium").value}/layout`),null,2);},"Layout loaded");}
  $("loadLayout").onclick=loadLayout;
  $("layoutForm").onsubmit=(event)=>{event.preventDefault();perform(async()=>{const seats=JSON.parse($("layoutJSON").value);await api(`/api/v1/admin/auditoriums/${$("layoutAuditorium").value}/layout`,{method:"PUT",body:JSON.stringify(seats)});},"Layout replaced");};

  function resetTier(){$("tierForm").reset();$("tierID").value="";$("tierActive").checked=true;}
  function renderTiers(){$("tierRows").innerHTML=state.tiers.map((v)=>`<tr><td>${esc(v.name)} ${statusBadge(v.active)}</td><td>${esc(cinemaName(v.cinema_id))}</td><td>${esc(v.seat_category)}</td><td class="actions"><button data-edit-tier="${v.id}">Edit</button><button class="danger" data-delete-tier="${v.id}">Archive</button></td></tr>`).join("")||`<tr><td colspan="4" class="empty">No pricing tiers</td></tr>`;}
  async function loadTiers(){state.tiers=await api("/api/v1/admin/pricing-tiers");renderTiers();}
  $("tierForm").onsubmit=(event)=>{event.preventDefault();perform(async()=>{const id=$("tierID").value;const body={cinema_id:$("tierCinema").value,name:$("tierName").value.trim(),seat_category:$("tierCategory").value.trim(),active:$("tierActive").checked};await api(id?`/api/v1/admin/pricing-tiers/${id}`:"/api/v1/admin/pricing-tiers",{method:id?"PUT":"POST",body:JSON.stringify(body)});resetTier();await loadTiers();},"Pricing tier saved");};
  $("tierReset").onclick=resetTier;
  $("tierRows").onclick=(event)=>{const edit=event.target.dataset.editTier,remove=event.target.dataset.deleteTier;if(edit){const v=state.tiers.find((item)=>item.id===edit);$("tierID").value=v.id;$("tierCinema").value=v.cinema_id;$("tierName").value=v.name;$("tierCategory").value=v.seat_category;$("tierActive").checked=v.active;}if(remove&&confirm("Archive this pricing tier?"))perform(async()=>{await api(`/api/v1/admin/pricing-tiers/${remove}`,{method:"DELETE"});await loadTiers();},"Pricing tier archived");};

  function resetShow(){$("showForm").reset();$("showID").value="";$("showCurrency").value="INR";$("showStatus").value="scheduled";$("showTierPrices").value="[]";}
  function renderShows(){$("showCount").textContent=`${state.shows.length} total`;$("showRows").innerHTML=state.shows.map((v)=>`<tr><td>${esc(movieName(v.movie_id))}</td><td>${esc(auditoriumName(v.auditorium_id))}</td><td>${new Date(v.starts_at).toLocaleString()}</td><td>${money(v.base_price_paise,v.currency)}</td><td><span class="badge">${esc(v.status)}</span></td><td class="actions"><button data-edit-show="${v.id}">Edit</button><button class="danger" data-delete-show="${v.id}">Cancel</button></td></tr>`).join("")||`<tr><td colspan="6" class="empty">No shows</td></tr>`;}
  async function loadShows(){state.shows=await api("/api/v1/admin/shows");renderShows();refreshSelects();}
  $("showForm").onsubmit=(event)=>{event.preventDefault();perform(async()=>{const id=$("showID").value;const body={movie_id:$("showMovie").value,auditorium_id:$("showAuditorium").value,starts_at:iso($("showStarts").value),sales_end_at:iso($("showSalesEnd").value),base_price_paise:Number($("showPrice").value),currency:$("showCurrency").value.trim(),status:$("showStatus").value,prices:JSON.parse($("showTierPrices").value||"[]")};await api(id?`/api/v1/admin/shows/${id}`:"/api/v1/admin/shows",{method:id?"PUT":"POST",body:JSON.stringify(body)});resetShow();await loadShows();},"Show saved");};
  $("showReset").onclick=resetShow;
  $("showRows").onclick=(event)=>{const edit=event.target.dataset.editShow,remove=event.target.dataset.deleteShow;if(edit){const v=state.shows.find((item)=>item.id===edit);$("showID").value=v.id;$("showMovie").value=v.movie_id;$("showAuditorium").value=v.auditorium_id;$("showStarts").value=localInput(v.starts_at);$("showSalesEnd").value=localInput(v.sales_end_at);$("showPrice").value=v.base_price_paise;$("showCurrency").value=v.currency;$("showStatus").value=v.status;$("showTierPrices").value=JSON.stringify(v.prices||[],null,2);}if(remove&&confirm("Cancel this show? Active bookings will prevent cancellation."))perform(async()=>{await api(`/api/v1/admin/shows/${remove}`,{method:"DELETE"});await loadShows();},"Show cancelled");};

  function renderBlocks(){$("blockRows").innerHTML=state.blocks.map((v)=>`<tr><td>${esc(auditoriumName(v.auditorium_id))} / <strong>${esc(v.seat_id)}</strong></td><td>${v.show_id?`Show ${esc(v.show_id)}`:"All shows"}</td><td>${esc(v.block_type)}<br><span class="muted">${esc(v.reason)}</span></td><td>${v.released_at?statusBadge(false):statusBadge(true)}</td><td>${v.released_at?"":`<button class="danger" data-release-block="${v.id}">Release</button>`}</td></tr>`).join("")||`<tr><td colspan="5" class="empty">No seat blocks</td></tr>`;}
  async function loadBlocks(){state.blocks=await api("/api/v1/admin/seat-blocks");renderBlocks();}
  $("loadBlocks").onclick=()=>perform(loadBlocks,"Seat blocks refreshed");
  $("blockForm").onsubmit=(event)=>{event.preventDefault();perform(async()=>{const show=$("blockShow").value;const body={auditorium_id:$("blockAuditorium").value,seat_id:$("blockSeat").value.trim(),block_type:$("blockType").value,reason:$("blockReason").value.trim()};if(show)body.show_id=show;if($("blockExpires").value)body.expires_at=iso($("blockExpires").value);await api("/api/v1/admin/seat-blocks",{method:"POST",body:JSON.stringify(body)});$("blockForm").reset();await loadBlocks();},"Seat blocked");};
  $("blockRows").onclick=(event)=>{const id=event.target.dataset.releaseBlock;if(id&&confirm("Release this seat block?"))perform(async()=>{await api(`/api/v1/admin/seat-blocks/${id}`,{method:"DELETE"});await loadBlocks();},"Seat block released");};

  function resetPromotion(){$("promotionForm").reset();$("promotionID").value="";$("promotionMinimum").value=0;$("promotionCustomerLimit").value=1;$("promotionActive").checked=true;}
  function renderPromotions(){$("promotionCount").textContent=`${state.promotions.length} total`;$("promotionRows").innerHTML=state.promotions.map((v)=>`<tr><td><strong>${esc(v.code)}</strong><br>${statusBadge(v.active)}</td><td>${v.cinema_id?esc(cinemaName(v.cinema_id)):"All cinemas"}</td><td>${v.discount_type==="percentage"?`${v.discount_value}%`:money(v.discount_value)}<br><span class="muted">Min ${money(v.minimum_subtotal_paise)}</span></td><td>${new Date(v.starts_at).toLocaleDateString()} – ${new Date(v.ends_at).toLocaleDateString()}</td><td>${v.redemption_count}${v.maximum_redemptions?` / ${v.maximum_redemptions}`:""}</td><td class="actions"><button data-edit-promotion="${v.id}">Edit</button><button class="danger" data-delete-promotion="${v.id}">Archive</button></td></tr>`).join("")||`<tr><td colspan="6" class="empty">No promotions</td></tr>`;}
  async function loadPromotions(){state.promotions=await api("/api/v1/admin/promotions");renderPromotions();}
  $("promotionForm").onsubmit=(event)=>{event.preventDefault();perform(async()=>{const id=$("promotionID").value;const cinema=$("promotionCinema").value;const body={code:$("promotionCode").value.trim(),name:$("promotionName").value.trim(),discount_type:$("promotionType").value,discount_value:Number($("promotionValue").value),minimum_subtotal_paise:Number($("promotionMinimum").value),maximum_discount_paise:nullableNumber($("promotionMaximum").value),starts_at:iso($("promotionStarts").value),ends_at:iso($("promotionEnds").value),maximum_redemptions:nullableNumber($("promotionRedemptions").value),per_customer_limit:Number($("promotionCustomerLimit").value),active:$("promotionActive").checked};if(cinema)body.cinema_id=cinema;await api(id?`/api/v1/admin/promotions/${id}`:"/api/v1/admin/promotions",{method:id?"PUT":"POST",body:JSON.stringify(body)});resetPromotion();await loadPromotions();},"Promotion saved");};
  $("promotionReset").onclick=resetPromotion;
  $("promotionRows").onclick=(event)=>{const edit=event.target.dataset.editPromotion,remove=event.target.dataset.deletePromotion;if(edit){const v=state.promotions.find((item)=>item.id===edit);$("promotionID").value=v.id;$("promotionCinema").value=v.cinema_id||"";$("promotionCode").value=v.code;$("promotionName").value=v.name;$("promotionType").value=v.discount_type;$("promotionValue").value=v.discount_value;$("promotionMinimum").value=v.minimum_subtotal_paise;$("promotionMaximum").value=v.maximum_discount_paise??"";$("promotionStarts").value=localInput(v.starts_at);$("promotionEnds").value=localInput(v.ends_at);$("promotionRedemptions").value=v.maximum_redemptions??"";$("promotionCustomerLimit").value=v.per_customer_limit;$("promotionActive").checked=v.active;}if(remove&&confirm("Archive this promotion?"))perform(async()=>{await api(`/api/v1/admin/promotions/${remove}`,{method:"DELETE"});await loadPromotions();},"Promotion archived");};

  function refreshSelects(){
    const cinemaOptions=optionList(state.cinemas.filter((v)=>v.active),(v)=>v.name,true);
    ["auditoriumCinema","tierCinema"].forEach((id)=>{const value=$(id).value;$(id).innerHTML=cinemaOptions;$(id).value=value;});
    const promotionValue=$("promotionCinema").value;$("promotionCinema").innerHTML=optionList(state.cinemas,(v)=>v.name,true,"All cinemas");$("promotionCinema").value=promotionValue;
    const auditoriumOptions=optionList(state.auditoriums.filter((v)=>v.active),(v)=>`${cinemaName(v.cinema_id)} — ${v.name}`,true);
    ["layoutAuditorium","showAuditorium","blockAuditorium"].forEach((id)=>{const value=$(id).value;$(id).innerHTML=auditoriumOptions;$(id).value=value;});
    const movieValue=$("showMovie").value;$("showMovie").innerHTML=optionList(state.movies.filter((v)=>v.active),(v)=>v.title,true);$("showMovie").value=movieValue;
    const showValue=$("blockShow").value;$("blockShow").innerHTML=optionList(state.shows,(v)=>`${movieName(v.movie_id)} — ${new Date(v.starts_at).toLocaleString()}`,true,"All shows");$("blockShow").value=showValue;
  }
  async function loadAdminData(){await Promise.all([loadMovies(),loadCinemas()]);await Promise.all([loadAuditoriums(),loadTiers(),loadShows(),loadBlocks(),loadPromotions()]);}
  async function loadCurrentPanel(){ if(hasRole("admin")) await loadAdminData(); }
  $("refresh").onclick=()=>perform(loadCurrentPanel,"Data refreshed");
  $("logout").onclick=async()=>{await api("/api/v1/auth/logout",{method:"POST"});location.href="/";};

  async function init(){
    try { state.user=await api("/api/v1/auth/me"); } catch { $("accessDenied").classList.remove("hidden");document.querySelectorAll(".panel,.topbar").forEach((el)=>el.classList.add("hidden"));return; }
    const allowed=hasRole("admin")||hasRole("support");
    if(!allowed){$("accessDenied").classList.remove("hidden");document.querySelectorAll(".panel,.topbar").forEach((el)=>el.classList.add("hidden"));return;}
    $("identity").textContent=`${state.user.email} · ${state.user.roles.join(", ")}`;
    if(!hasRole("admin")){document.querySelectorAll("[data-admin-only]").forEach((el)=>el.classList.add("hidden"));document.querySelector('button[data-panel="support"]').click();}
    if(hasRole("admin")) await loadAdminData();
  }
  init().catch((error)=>message(error.message,"error"));
})();
