(() => {
  const state = {
    user: null,
    screening: null,
    seats: [],
    selected: new Set(),
    seatEvents: null,
  };
  const $ = (id) => document.getElementById(id);
  async function api(path, options = {}) {
    options.headers = {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    };
    let response;
    try {
      response = await fetch(path, options);
    } catch (error) {
      if (!options.method || options.method === "GET") {
        await new Promise((resolve) => setTimeout(resolve, 500));
        response = await fetch(path, options);
      } else {
        throw new Error("Network unavailable. Check your connection and retry.");
      }
    }
    if (response.status === 204) return null;
    const data = await response
      .json()
      .catch(() => ({ error: "Invalid server response" }));
    if (response.status === 401) {
      state.user = null;
      showSignedOut();
      throw new Error("Your session expired. Sign in again to continue.");
    }
    if (!response.ok) throw new Error(data.error || "Request failed");
    return data;
  }
  function message(text, type = "") {
    const el = $("message");
    el.textContent = text;
    el.className = type;
  }
  function showRetry(root, text, action) {
    root.innerHTML = "";
    const label = document.createElement("p");
    label.className = "error";
    label.textContent = text;
    const retry = document.createElement("button");
    retry.type = "button";
    retry.textContent = "Retry";
    retry.onclick = action;
    root.append(label, retry);
  }
  async function loadIdentity() {
    try {
      state.user = await api("/api/v1/auth/me");
      $("identity").textContent = state.user.email;
      $("auth").classList.add("hidden");
      $("logout").classList.remove("hidden");
      $("bookings").classList.remove("hidden");
      $("phone").value = state.user.phone || "";
      renderVerification();
      await loadBookings();
    } catch {
      state.user = null;
      showSignedOut();
    }
  }
  function showSignedOut() {
    $("identity").textContent = "Not signed in";
    $("logout").classList.add("hidden");
    $("auth").classList.remove("hidden");
    $("bookings").classList.add("hidden");
  }
  $("logout").onclick = async () => {
    await api("/api/v1/auth/logout", { method: "POST" });
    state.user = null;
    showSignedOut();
    message("Signed out", "success");
  };
  $("authForm").addEventListener("click", async (event) => {
    if (event.target.tagName !== "BUTTON") return;
    event.preventDefault();
    try {
      state.user = await api(`/api/v1/auth/${event.target.dataset.action}`, {
        method: "POST",
        body: JSON.stringify({
          email: $("email").value,
          password: $("password").value,
        }),
      });
      $("identity").textContent = state.user.email;
      $("auth").classList.add("hidden");
      $("logout").classList.remove("hidden");
      $("bookings").classList.remove("hidden");
      $("phone").value = state.user.phone || "";
      renderVerification();
      await loadBookings();
      message("Signed in", "success");
    } catch (error) {
      message(error.message, "error");
    }
  });
  const resetToken = new URLSearchParams(location.search).get("reset_token");
  const emailVerificationToken = new URLSearchParams(location.search).get("verify_email");
  function openReset() {
    $("resetPanel").classList.remove("hidden");
    $("resetEmail").classList.toggle("hidden", Boolean(resetToken));
    $("resetPassword").classList.toggle("hidden", !resetToken);
    $("resetTitle").textContent = resetToken ? "Choose a new password" : "Request password reset";
    (resetToken ? $("resetPassword") : $("resetEmail")).focus();
  }
  $("forgotPassword").onclick = openReset;
  $("closeReset").onclick = () => $("resetPanel").classList.add("hidden");
  $("resetForm").onsubmit = async (event) => {
    event.preventDefault();
    try {
      if (resetToken) {
        await api("/api/v1/auth/password-reset/confirm", { method: "POST", body: JSON.stringify({ token: resetToken, password: $("resetPassword").value }) });
        history.replaceState({}, "", "/");
        message("Password updated. Sign in with your new password.", "success");
      } else {
        const result = await api("/api/v1/auth/password-reset/request", { method: "POST", body: JSON.stringify({ email: $("resetEmail").value }) });
        message(result.message, "success");
      }
      $("resetPanel").classList.add("hidden");
    } catch (error) { message(error.message, "error"); }
  };
  if (resetToken) openReset();
  if (emailVerificationToken) {
    api("/api/v1/auth/email-verification/confirm", { method: "POST", body: JSON.stringify({ token: emailVerificationToken }) })
      .then(() => {
        history.replaceState({}, "", "/");
        message("Email address verified. Sign in to refresh your notification status.", "success");
      })
      .catch((error) => message(error.message, "error"));
  }
  if (new URLSearchParams(location.search).has("oauth_error")) message("Google sign-in could not be completed.", "error");
  api("/api/v1/client-config").then((config) => $("googleLogin").classList.toggle("hidden", !config.google_login)).catch(() => {});
  let screeningRequest = 0;
  async function loadScreenings() {
    const request = ++screeningRequest;
    const root = $("screenings");
    root.setAttribute("aria-busy", "true");
    if (!root.children.length) root.innerHTML = '<p class="muted">Loading screenings…</p>';
    try {
      const params = new URLSearchParams();
      const filters = {
        q: $("movieFilter").value,
        city: $("cityFilter").value,
        date: $("dateFilter").value,
        language: $("languageFilter").value,
        format: $("formatFilter").value,
      };
      Object.entries(filters).forEach(([key, value]) => {
        if (value.trim()) params.set(key, value.trim());
      });
      const items = await api(`/api/v1/screenings?${params}`);
      if (request !== screeningRequest) return;
      root.innerHTML = "";
      if (!items.length) {
        root.innerHTML = '<p class="muted">No screenings match these filters.</p>';
      }
      items.forEach((item) => {
        const button = document.createElement("button");
        button.className = "screening";
        button.innerHTML = `<strong>${escapeHTML(item.movie_title)}</strong><br><span class="muted">${new Date(item.starts_at).toLocaleString()} · ${escapeHTML(item.cinema_name)}, ${escapeHTML(item.cinema_city)} · ${escapeHTML(item.screen_name)} · ${escapeHTML(item.format)} · ₹${(item.base_price_paise / 100).toFixed(2)}</span>`;
        button.onclick = () => selectScreening(item, button);
        root.append(button);
      });
    } catch (error) {
      if (request !== screeningRequest) return;
      message(error.message, "error");
      showRetry(root, "Screenings could not be loaded.", loadScreenings);
    } finally {
      root.setAttribute("aria-busy", "false");
    }
  }
  let filterTimer;
  $("filters").addEventListener("input", () => {
    clearTimeout(filterTimer);
    filterTimer = setTimeout(loadScreenings, 250);
  });
  $("filters").addEventListener("reset", () => setTimeout(loadScreenings));
  $("filters").addEventListener("submit", (event) => {
    event.preventDefault();
    clearTimeout(filterTimer);
    loadScreenings();
  });
  async function selectScreening(item, button) {
    state.screening = item;
    state.selected.clear();
    document
      .querySelectorAll(".screening")
      .forEach((x) => x.classList.remove("selected"));
    button.classList.add("selected");
    $("booking").classList.remove("hidden");
    $("screeningTitle").textContent = item.movie_title;
    await loadSeats();
    subscribeToSeats(item.id);
  }
  function subscribeToSeats(screeningID) {
    if (state.seatEvents) state.seatEvents.close();
    const source = new EventSource(`/api/v1/screenings/${screeningID}/seat-events`);
    state.seatEvents = source;
    source.addEventListener("seats", (event) => {
      if (state.screening?.id !== screeningID) return;
      state.seats = JSON.parse(event.data);
      for (const id of [...state.selected]) {
        if (state.seats.find((seat) => seat.id === id)?.status !== "available") {
          state.selected.delete(id);
        }
      }
      renderSeats();
    });
    source.onerror = () => message("Seat updates interrupted; reconnecting…", "error");
  }
  async function loadSeats() {
    try {
      state.seats = await api(`/api/v1/screenings/${state.screening.id}/seats`);
      renderSeats();
    } catch (error) {
      message(error.message, "error");
      showRetry($("seats"), "Seat availability could not be loaded.", loadSeats);
    }
  }
  function renderSeats() {
    const root = $("seats");
    root.innerHTML = "";
    state.seats.forEach((seat) => {
      const button = document.createElement("button");
      button.textContent = seat.id;
      button.className = `seat ${seat.status}`;
      button.disabled = seat.status !== "available";
      button.type = "button";
      button.setAttribute("aria-label", `Seat ${seat.id}: ${seat.status.replaceAll("_", " ")}`);
      button.setAttribute("aria-pressed", state.selected.has(seat.id) ? "true" : "false");
      if (state.selected.has(seat.id)) button.classList.add("selected");
      button.onclick = () => {
        state.selected.has(seat.id)
          ? state.selected.delete(seat.id)
          : state.selected.add(seat.id);
        renderSeats();
      };
      root.append(button);
    });
    const count = state.selected.size;
    $("selection").textContent = count
      ? `${[...state.selected].join(", ")} (${count} seat${count > 1 ? "s" : ""})`
      : "No seats selected";
    const subtotal = count * state.screening.base_price_paise;
    const fee = Math.ceil((subtotal * 300) / 10000);
    const tax = Math.ceil((fee * 1800) / 10000);
    $("price").innerHTML = count
      ? `<span>Tickets</span><span>₹${money(subtotal)}</span><span>Convenience fee</span><span>₹${money(fee)}</span><span>GST on fee</span><span>₹${money(tax)}</span><strong>Total</strong><strong>₹${money(subtotal + fee + tax)}</strong>`
      : "";
    $("checkout").disabled = !count;
  }
  $("checkout").onclick = async () => {
    if (!state.user) {
      message("Sign in before booking", "error");
      return;
    }
    $("checkout").disabled = true;
    try {
      const hold = await api(`/api/v1/screenings/${state.screening.id}/holds`, {
        method: "POST",
        body: JSON.stringify({ seat_ids: [...state.selected] }),
      });
      const result = await api(
        `/api/v1/screenings/${state.screening.id}/holds/${hold.id}/checkout`,
        { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() } },
      );
      openPayment(result);
    } catch (error) {
      message(error.message, "error");
      await loadSeats();
    }
  };
  function openPayment(result) {
    const order = result.order;
    const instance = new Razorpay({
      key: result.checkout_key,
      amount: order.amount_paise,
      currency: order.currency,
      order_id: order.provider_order_id,
      name: "Cinema Booking",
      description: `Seats ${order.seat_ids.join(", ")}`,
      prefill: { email: state.user.email },
      handler: async (response) => {
        try {
          await api(`/api/v1/orders/${order.id}/verify-payment`, {
            method: "POST",
            body: JSON.stringify(response),
          });
          message("Payment received. Confirming your ticket…", "success");
          pollOrder(order.id);
        } catch (error) {
          message(error.message, "error");
        }
      },
      modal: {
        ondismiss: () =>
          message(
            "Payment window closed. Your hold will expire automatically.",
          ),
      },
      theme: { color: "#65d1ff" },
    });
    instance.open();
  }
  async function pollOrder(id) {
    for (let i = 0; i < 15; i++) {
      await new Promise((resolve) => setTimeout(resolve, 2000));
      const order = await api(`/api/v1/orders/${id}`);
      if (order.status === "confirmed") {
        message(`Booking confirmed: ${order.seat_ids.join(", ")}`, "success");
        state.selected.clear();
        await loadSeats();
        await loadBookings();
        return;
      }
      if (
        ["payment_failed", "expired", "refund_pending", "refunded"].includes(
          order.status,
        )
      ) {
        message(`Payment status: ${order.status}`, "error");
        return;
      }
    }
    message(
      "Payment is still processing. Your order remains available in the API.",
    );
  }
  async function loadBookings() {
    if (!state.user) return;
    const root = $("bookingList");
    root.setAttribute("aria-busy", "true");
    if (!root.children.length) root.innerHTML = '<p class="muted">Loading bookings…</p>';
    try {
      const bookings = await api("/api/v1/bookings");
      root.innerHTML = bookings.length ? "" : '<p class="muted">No bookings yet.</p>';
      bookings.forEach((booking) => {
        const card = document.createElement("article");
        card.className = "booking-card";
        card.innerHTML = `<h3>${escapeHTML(booking.movie_title)}</h3><p>${new Date(booking.starts_at).toLocaleString()} · ${escapeHTML(booking.cinema_name)}, ${escapeHTML(booking.cinema_city)} · ${escapeHTML(booking.screen_name)} · ${escapeHTML(booking.format)}</p><p>Seats <strong>${booking.seat_ids.map(escapeHTML).join(", ")}</strong> · ₹${money(booking.amount_paise)} · <strong>${escapeHTML(booking.status.replaceAll("_", " "))}</strong></p>`;
        const actions = document.createElement("div");
        actions.className = "row";
        if (booking.status === "confirmed") {
          const ticket = document.createElement("a");
          ticket.className = "button";
          ticket.href = `/api/v1/bookings/${booking.id}/ticket`;
          ticket.textContent = "Download ticket";
          actions.append(ticket);
        }
        if (booking.can_cancel) {
		  const policy = document.createElement("p");
		  policy.className = "muted";
		  policy.textContent = `Cancel at least ${formatMinutes(booking.cancellation_cutoff_minutes)} before showtime for a ${booking.cancellation_refund_percent}% refund (₹${money(Math.floor(booking.amount_paise * booking.cancellation_refund_percent / 100))}).`;
		  card.append(policy);
          const cancel = document.createElement("button");
          cancel.type = "button";
          cancel.textContent = "Cancel & refund";
          cancel.onclick = () => cancelBooking(booking.id, cancel);
          actions.append(cancel);
        }
        card.append(actions);
        root.append(card);
      });
    } catch (error) { message(error.message, "error"); showRetry(root, "Bookings could not be loaded.", loadBookings); }
    finally { root.setAttribute("aria-busy", "false"); }
  }
  async function cancelBooking(id, button) {
    const reason = prompt("Why are you cancelling this booking?");
    if (!reason?.trim()) return;
    button.disabled = true;
    try {
      await api(`/api/v1/bookings/${id}/cancel`, { method: "POST", body: JSON.stringify({ reason: reason.trim() }) });
      message("Cancellation accepted. Your refund is being processed.", "success");
      await Promise.all([loadBookings(), state.screening ? loadSeats() : Promise.resolve()]);
    } catch (error) { message(error.message, "error"); button.disabled = false; }
  }
  $("refreshBookings").onclick = loadBookings;
  $("profileForm").onsubmit = async (event) => {
    event.preventDefault();
    try {
      state.user = await api("/api/v1/auth/profile", { method: "PATCH", body: JSON.stringify({ phone: $("phone").value }) });
      renderVerification();
      message("Notification preferences saved.", "success");
    } catch (error) { message(error.message, "error"); }
  };
  function renderVerification() {
    if (!state.user) return;
    $("emailVerificationStatus").textContent = state.user.email_verified
      ? `Email verified: ${state.user.email}`
      : `Email not verified: ${state.user.email}`;
    $("sendEmailVerification").classList.toggle("hidden", state.user.email_verified);
    $("phoneVerificationStatus").textContent = !state.user.phone
      ? "Add and save a phone number before requesting an SMS code."
      : state.user.phone_verified
        ? `Phone verified: ${state.user.phone}`
        : `Phone not verified: ${state.user.phone}`;
    $("sendPhoneVerification").disabled = !state.user.phone || state.user.phone_verified;
    $("phoneCode").classList.toggle("hidden", !state.user.phone || state.user.phone_verified);
    $("confirmPhone").classList.toggle("hidden", !state.user.phone || state.user.phone_verified);
  }
  $("sendEmailVerification").onclick = async () => {
    try {
      const result = await api("/api/v1/auth/email-verification/request", { method: "POST" });
      message(result.message, "success");
    } catch (error) { message(error.message, "error"); }
  };
  $("sendPhoneVerification").onclick = async () => {
    try {
      const result = await api("/api/v1/auth/phone-verification/request", { method: "POST" });
      message(result.message, "success");
      $("phoneCode").focus();
    } catch (error) { message(error.message, "error"); }
  };
  $("confirmPhone").onclick = async () => {
    try {
      await api("/api/v1/auth/phone-verification/confirm", { method: "POST", body: JSON.stringify({ code: $("phoneCode").value }) });
      state.user = await api("/api/v1/auth/me");
      renderVerification();
      message("Phone number verified.", "success");
    } catch (error) { message(error.message, "error"); }
  };
  function money(paise) { return (paise / 100).toFixed(2); }
  function formatMinutes(minutes) {
    if (minutes < 60) return `${minutes} minutes`;
    const hours = minutes / 60;
    return `${Number.isInteger(hours) ? hours : hours.toFixed(1)} hours`;
  }
  function escapeHTML(value) {
    const div = document.createElement("div");
    div.textContent = value;
    return div.innerHTML;
  }
  let wasOnline = navigator.onLine;
  function updateConnectivity() {
    const online = navigator.onLine;
    $("offline").classList.toggle("hidden", online);
    if (online && !wasOnline) message("Connection restored. You can retry your last action.", "success");
    wasOnline = online;
  }
  addEventListener("online", updateConnectivity);
  addEventListener("offline", updateConnectivity);
  updateConnectivity();
  let installPrompt;
  addEventListener("beforeinstallprompt", (event) => {
    event.preventDefault();
    installPrompt = event;
    $("installApp").classList.remove("hidden");
  });
  $("installApp").onclick = async () => {
    if (!installPrompt) return;
    await installPrompt.prompt();
    installPrompt = null;
    $("installApp").classList.add("hidden");
  };
  addEventListener("appinstalled", () => {
    installPrompt = null;
    $("installApp").classList.add("hidden");
    message("Cinema Booking was installed.", "success");
  });
  if ("serviceWorker" in navigator) {
    addEventListener("load", () => navigator.serviceWorker.register("/sw.js").catch(() => {
      message("Offline support could not be enabled on this device.", "error");
    }));
  }
  Promise.all([loadIdentity(), loadScreenings()]);
})();
