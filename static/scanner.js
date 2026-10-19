(() => {
  const $ = (id) => document.getElementById(id);
  let stream;
  let detector;
  let scanning = false;
  let validationPending = false;
  let lastCode = "";
  let lastScanAt = 0;

  const savedKey = sessionStorage.getItem("cinema_scanner_key");
  if (savedKey) {
    $("scannerKey").value = savedKey;
    $("rememberKey").checked = true;
  }

  $("rememberKey").onchange = saveKey;
  $("scannerKey").oninput = saveKey;
  function saveKey() {
    if ($("rememberKey").checked && $("scannerKey").value) {
      sessionStorage.setItem("cinema_scanner_key", $("scannerKey").value);
    } else {
      sessionStorage.removeItem("cinema_scanner_key");
    }
  }

  $("startCamera").onclick = startCamera;
  $("stopCamera").onclick = stopCamera;
  $("manualForm").onsubmit = (event) => {
    event.preventDefault();
    validate($("ticketCode").value.trim());
  };

  async function startCamera() {
    if (
      !("BarcodeDetector" in window) ||
      !navigator.mediaDevices?.getUserMedia
    ) {
      show(
        "Camera scanning unavailable",
        "Use the manual code field on this browser.",
        "error",
      );
      return;
    }
    try {
      detector = new BarcodeDetector({ formats: ["qr_code"] });
      stream = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: { ideal: "environment" } },
        audio: false,
      });
      $("camera").srcObject = stream;
      await $("camera").play();
      scanning = true;
      $("camera").classList.remove("hidden");
      $("startCamera").classList.add("hidden");
      $("stopCamera").classList.remove("hidden");
      $("cameraHelp").textContent = "Point the camera at the ticket QR code.";
      scanFrame();
    } catch (error) {
      show(
        "Camera could not start",
        error.message || "Check camera permission and retry.",
        "error",
      );
      stopCamera();
    }
  }

  async function scanFrame() {
    if (!scanning) return;
    try {
      const codes = await detector.detect($("camera"));
      const value = codes[0]?.rawValue?.trim();
      if (
        value &&
        !validationPending &&
        (value !== lastCode || Date.now() - lastScanAt > 5000)
      ) {
        lastCode = value;
        lastScanAt = Date.now();
        $("ticketCode").value = value;
        await validate(value);
      }
    } catch (_) {
      // A video frame can be unavailable while the camera changes exposure.
    }
    if (scanning) setTimeout(scanFrame, 300);
  }

  function stopCamera() {
    scanning = false;
    stream?.getTracks().forEach((track) => track.stop());
    stream = null;
    $("camera").srcObject = null;
    $("camera").classList.add("hidden");
    $("startCamera").classList.remove("hidden");
    $("stopCamera").classList.add("hidden");
  }

  async function validate(code) {
    const key = $("scannerKey").value.trim();
    if (!key) {
      show(
        "Scanner key required",
        "Enter the staff scanner API key before validating tickets.",
        "error",
      );
      $("scannerKey").focus();
      return;
    }
    if (!code || validationPending) return;
    validationPending = true;
    show(
      "Validating…",
      "Checking the signed ticket with the cinema server.",
      "",
    );
    try {
      const response = await fetch("/api/v1/tickets/validate", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Scanner-Key": key },
        body: JSON.stringify({ code }),
      });
      const data = await response
        .json()
        .catch(() => ({ error: "Invalid server response" }));
      if (!response.ok)
        throw new Error(data.error || "Ticket validation failed");
      show(
        "Admission approved",
        `Order ${data.order_id} admitted at ${new Date(data.admitted_at).toLocaleString()}.`,
        "success",
      );
      navigator.vibrate?.(100);
    } catch (error) {
      const offline = !navigator.onLine ? " The scanner is offline." : "";
      show("Admission denied", error.message + offline, "error");
      navigator.vibrate?.([100, 70, 100]);
    } finally {
      validationPending = false;
    }
  }

  function show(title, detail, type) {
    const root = $("result");
    root.className = `panel ${type}`;
    root.innerHTML = "";
    const heading = document.createElement("h2");
    heading.textContent = title;
    const text = document.createElement("p");
    text.textContent = detail;
    root.append(heading, text);
  }

  addEventListener("pagehide", stopCamera);
})();
