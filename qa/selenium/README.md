# Selenium browser suite

This Maven module runs Java 17, Selenium WebDriver, JUnit 5, and WebDriverManager against a running cinema app. Tests use page objects and explicit waits. Each test opens a fresh browser. Account tests use a unique customer; other signed-in tests share one suite account to stay below the application's per-IP registration limit. The suite expects at least one future screening with available seats, as supplied by a fresh Compose database.

## Run locally

From the repository root, start the app and wait for `http://localhost:8080/health/ready`:

```sh
docker compose up --build -d
cd qa/selenium
mvn test -DbaseUrl=http://localhost:8080 -Dheadless=true
```

Java 17, Maven, Chrome or Chromium, and a working ChromeDriver download are required for local execution. WebDriverManager resolves ChromeDriver. If Chromium is outside the normal browser path, pass `-Dchrome.binary=/path/to/chromium`. To use a Selenium Grid instead, pass `-DremoteUrl=http://grid-host:4444/wd/hub`; the browser in the Grid must be able to reach `baseUrl`. Pass `-Dheadless=false` to watch local tests. Set `-DscannerKey=...` to run the authenticated malformed-ticket scanner test; it is skipped when this property is absent. CI supplies the local Compose scanner key.

Failures write PNG screenshots and browser console logs to `target/failure-artifacts/`; Maven writes JUnit XML reports to `target/surefire-reports/`. CI uploads both directories on failure. Override the artifact directory with `-DartifactsDir=/path/to/artifacts`.

Confirmed-booking and successful-scanner tests create short-lived records directly in the test PostgreSQL database and delete them after each test. The default connection is the local Compose database at `jdbc:postgresql://localhost:5433/cinema` with its documented local credentials. For another isolated stack, set `-Dfixture.jdbcUrl=...`, `-Dfixture.dbUser=...`, and `-Dfixture.dbPassword=...`. Set `-DticketSigningSecret=...` to match the test app when overriding its signing secret. The Maven process must be able to reach both the app and this database.

The seat test uses the signed-in browser session to create a hold through the application's own API, then verifies that the customer UI updates and another hold gets HTTP 409. It stops before checkout because checkout requires a live Razorpay test account. A hold expires according to the app's configured TTL. Scanner tests cover missing and bad keys and, when configured, a malformed ticket and a signed confirmed ticket with a valid key. The confirmed booking is a database fixture, so this suite does not prove payment confirmation or Razorpay integration. Camera scanning requires dedicated hardware and is outside this CI suite.

Run against an isolated test stack: tests create accounts and temporary holds, and a preexisting account or held seat can affect assertions. Do not point `baseUrl` at production.
