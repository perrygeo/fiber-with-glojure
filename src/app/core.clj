(ns app.core)

(defn hello [request]
  (let [name (get-in request [:params :name] "world")]
    {:status 200
     :content-type "text/html"
     :body (str "<h1>Hello, " name "!</h1>")}))

(defn not-found [_request]
  {:status 404
   :content-type "text/plain"
   :body "Not found"})

(defn handler [request]
  (case (:path request)
    "/" (hello request)
    (not-found request)))
