package com.cinema.qa.support;

import java.util.List;
import java.util.Map;
import org.openqa.selenium.JavascriptExecutor;
import org.openqa.selenium.WebDriver;

/** Uses the browser's session cookie for fixture setup and server-side boundary checks. */
public final class BrowserApi {
    private final JavascriptExecutor scripts;

    public BrowserApi(WebDriver driver) {
        this.scripts = (JavascriptExecutor) driver;
    }

    @SuppressWarnings("unchecked")
    public Map<String, Object> request(String path, String method, Object body) {
        return (Map<String, Object>) scripts.executeAsyncScript("""
            const [path, method, body, done] = arguments;
            const options = {method, headers: {'Content-Type': 'application/json'}};
            if (body !== null) options.body = JSON.stringify(body);
            fetch(path, options).then(async response => {
              let data;
              try { data = await response.json(); } catch (_) { data = null; }
              done({status: response.status, body: data});
            }).catch(error => done({status: 0, body: {error: error.message}}));
            """, path, method, body);
    }

    @SuppressWarnings("unchecked")
    public List<Map<String, Object>> screenings() {
        return (List<Map<String, Object>>) request("/api/v1/screenings", "GET", null).get("body");
    }

    @SuppressWarnings("unchecked")
    public List<Map<String, Object>> seats(String screeningId) {
        return (List<Map<String, Object>>) request("/api/v1/screenings/" + screeningId + "/seats", "GET", null).get("body");
    }

    public Map<String, Object> hold(String screeningId, String seatId) {
        return request("/api/v1/screenings/" + screeningId + "/holds", "POST",
                Map.of("seat_ids", List.of(seatId)));
    }
}
